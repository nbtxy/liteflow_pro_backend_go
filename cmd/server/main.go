package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/liteflow/backend/internal/admin"
	"github.com/liteflow/backend/internal/agent"
	"github.com/liteflow/backend/internal/agent_profile"
	"github.com/liteflow/backend/internal/api"
	"github.com/liteflow/backend/internal/artifact"
	"github.com/liteflow/backend/internal/auth"
	"github.com/liteflow/backend/internal/channel"
	"github.com/liteflow/backend/internal/chat"
	"github.com/liteflow/backend/internal/config"
	"github.com/liteflow/backend/internal/conversation"
	"github.com/liteflow/backend/internal/feedback"
	"github.com/liteflow/backend/internal/imagegen"
	"github.com/liteflow/backend/internal/llm"
	"github.com/liteflow/backend/internal/maintenance"
	"github.com/liteflow/backend/internal/mcp"
	"github.com/liteflow/backend/internal/memory"
	"github.com/liteflow/backend/internal/platform/postgres"
	platformsearch "github.com/liteflow/backend/internal/platform/search"
	"github.com/liteflow/backend/internal/platform/sms"
	"github.com/liteflow/backend/internal/platform/storage"
	"github.com/liteflow/backend/internal/skill"
	taskpkg "github.com/liteflow/backend/internal/task"
	"github.com/liteflow/backend/internal/tool"
	"github.com/liteflow/backend/internal/usage"
	"github.com/liteflow/backend/internal/user"
)

func main() {
	// Load .env
	godotenv.Load()

	// Parse config
	var cfg config.Config
	if err := env.Parse(&cfg); err != nil {
		slog.Error("failed to parse config", "err", err)
		os.Exit(1)
	}

	// Setup structured logging
	logLevel := slog.LevelInfo
	if cfg.Env == "dev" {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))

	slog.Info("starting server", "env", cfg.Env, "port", cfg.Port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Database
	if err := postgres.Migrate(cfg.DB); err != nil {
		slog.Error("migration failed", "err", err)
		os.Exit(1)
	}

	pool, err := postgres.NewPool(ctx, cfg.DB)
	if err != nil {
		slog.Error("database connection failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// SMS
	var smsSvc sms.Service
	if cfg.SMS.Provider == "aliyun" {
		smsSvc = sms.NewAliyun(cfg.SMS)
	} else {
		smsSvc = sms.NewMock()
	}

	// JWT & Auth
	jwtSvc := auth.NewJwtService(cfg.JWT)
	authSvc := auth.NewAuthService(pool, jwtSvc, smsSvc, cfg.JWT, cfg.SuperAdminPhone)

	// LLM Providers
	providerRouter := llm.NewProviderRouter(cfg.LLM.DefaultProvider)
	if cfg.LLM.DeepSeek.APIKey != "" {
		providerRouter.Register(llm.NewDeepSeekProvider(cfg.LLM.DeepSeek))
	}
	if cfg.LLM.Qwen.APIKey != "" {
		providerRouter.Register(llm.NewQwenProvider(cfg.LLM.Qwen))
	}
	if cfg.LLM.OpenRouter.APIKey != "" {
		providerRouter.Register(llm.NewOpenRouterProvider(cfg.LLM.OpenRouter))
	}
	if cfg.LLM.Cloudflare.Endpoint != "" {
		providerRouter.Register(llm.NewCloudflareProvider(cfg.LLM.Cloudflare))
	}

	// Image Generation Providers
	imageProviderRouter := imagegen.NewProviderRouter("openrouter-image")
	if cfg.LLM.Cloudflare.Endpoint != "" && cfg.LLM.Cloudflare.Token != "" {
		imageProviderRouter.Register(imagegen.NewCloudflareGeminiProvider(cfg.LLM.Cloudflare))
		imageProviderRouter.Register(imagegen.NewCloudflareOpenAIImageProvider(cfg.LLM.Cloudflare))
	}
	if cfg.LLM.OpenRouter.APIKey != "" {
		imageProviderRouter.Register(imagegen.NewOpenRouterImageProvider(cfg.LLM.OpenRouter, cfg.LLM.OpenRouterImageModel, cfg.LLM.Cloudflare.Endpoint, cfg.LLM.Cloudflare.Token))
	}

	// Services
	if cfg.Storage.AliyunEndpoint == "" ||
		cfg.Storage.AliyunRegion == "" ||
		cfg.Storage.AliyunBucket == "" ||
		cfg.Storage.AliyunKeyID == "" ||
		cfg.Storage.AliyunSecret == "" {
		slog.Error("OSS storage config is required (endpoint/region/bucket/key)")
		os.Exit(1)
	}
	storageSvc, err := storage.NewOSS(
		cfg.Storage.AliyunEndpoint,
		cfg.Storage.AliyunBucket,
		cfg.Storage.AliyunKeyID,
		cfg.Storage.AliyunSecret,
	)
	if err != nil {
		slog.Error("failed to initialize OSS storage", "err", err)
		os.Exit(1)
	}
	localCacheStorage := storage.NewLocal(cfg.Storage.BasePath)
	stsProvider, err := storage.NewSTSProvider(
		cfg.Storage.AliyunRegion,
		cfg.Storage.AliyunEndpoint,
		cfg.Storage.AliyunBucket,
		cfg.Storage.AliyunKeyID,
		cfg.Storage.AliyunSecret,
		cfg.Storage.AliyunStsRole,
		cfg.Storage.AliyunStsTTL,
	)
	if err != nil {
		slog.Error("failed to initialize OSS STS provider", "err", err)
		os.Exit(1)
	}
	var convSvc *conversation.Service
	usageSvc := usage.NewService(pool)
	artifactSvc := artifact.NewService(pool)
	searchRouter := platformsearch.NewProviderRouter(cfg.Search.Provider)
	if cfg.Search.Metaso.APIKey != "" {
		searchRouter.Register(platformsearch.NewMetaso(
			cfg.Search.Metaso.APIKey,
			cfg.Search.Metaso.Endpoint,
			cfg.Search.Metaso.Timeout,
			cfg.Search.Metaso.Count,
		))
	}
	if cfg.Search.Tavily.APIKey != "" {
		searchRouter.Register(platformsearch.NewTavily(
			cfg.Search.Tavily.APIKey,
			cfg.Search.Tavily.Endpoint,
			cfg.Search.Tavily.Timeout,
			cfg.Search.Tavily.Count,
		))
	}
	ossLinkSvc := storageSvc
	convSvc = conversation.NewService(pool, storageSvc, ossLinkSvc)
	memorySvc := memory.NewService(pool)
	feedbackSvc := feedback.NewService(pool)
	userSvc := user.NewService(pool)
	adminSvc := admin.NewService(pool)
	mcpClient := mcp.NewClient()
	mcpSvc := mcp.NewService(pool)
	channelMgr := channel.NewManager(pool, mcpClient)
	oauthSvc := mcp.NewOAuthService(cfg.MCP)
	mcpClient.SetTokenRefreshFunc(func(ctx context.Context, serverCfg mcp.ServerConfig) (mcp.ServerConfig, error) {
		return mcpSvc.RefreshChannelToken(ctx, serverCfg, oauthSvc)
	})
	skillRegistry := skill.NewRegistry()

	// Task System (before tool registry so the tool can reference it)
	taskExecutor := taskpkg.NewExecutor(pool, cfg.Tasks)
	taskExecutor.SetProvider(providerRouter)
	outputSender := taskpkg.NewOutputSender(pool, nil)
	taskExecutor.SetOutputSender(outputSender)
	taskScheduler := taskpkg.NewScheduler(pool, cfg.Tasks, taskExecutor)

	// Tool Registry
	toolRegistry := tool.NewRegistry()
	toolRegistry.Register(tool.NewCalculator())
	toolRegistry.Register(tool.NewWebSearch(searchRouter))
	toolRegistry.Register(tool.NewActiveMCP(mcpSvc.ActivateByPlatform))
	toolRegistry.Register(tool.NewMemoryManage(memorySvc.ManageTool))
	toolRegistry.Register(tool.NewSearchSkill(skillRegistry.Search, func() []string {
		all := skillRegistry.All()
		names := make([]string, 0, len(all))
		for _, s := range all {
			if s != nil && s.Name != "" {
				names = append(names, s.Name)
			}
		}
		return names
	}))
	toolRegistry.Register(tool.NewManageScheduledTask(taskScheduler.ManageTool))
	toolRegistry.Register(tool.NewCreateFile(storageSvc, artifactSvc.CreateFileArtifact))
	toolRegistry.Register(tool.NewStrReplace(storageSvc, localCacheStorage, artifactSvc.CreateFileArtifact))
	toolRegistry.Register(tool.NewView(storageSvc, artifactSvc.GetLatestArtifacts))
	toolRegistry.Register(tool.NewHttpRequest())
	toolRegistry.Register(tool.NewDownloadFile(storageSvc, artifactSvc.CreateFileArtifact))
	if _, err := providerRouter.Get("qwen"); err == nil {
		toolRegistry.Register(tool.NewAnalyzeImage(storageSvc, ossLinkSvc, providerRouter))
	} else {
		slog.Info("analyze_image tool disabled (qwen provider unavailable)")
	}
	if _, err := imageProviderRouter.Get("cloudflare-gemini-image"); err == nil {
		toolRegistry.Register(tool.NewNanoBananaImageGenerate(
			storageSvc,
			ossLinkSvc,
			artifactSvc.CreateImageArtifact,
			artifactSvc.UpdateMetadataByID,
			func(ctx context.Context, userID, conversationID, messageID uuid.UUID, tokenUsage *llm.LlmUsage) {
				if usageSvc == nil || tokenUsage == nil || tokenUsage.TotalTokens() <= 0 {
					return
				}
				usageSvc.RecordAsync(
					ctx,
					userID,
					conversationID,
					messageID,
					cfg.LLM.Cloudflare.ImageModel,
					tokenUsage,
					"tool_generate_or_edit_image_nano_banana",
					"tool",
					0,
				)
			},
			imageProviderRouter,
			cfg.LLM.Cloudflare.ImageModel,
		))
	} else {
		slog.Info("generate_or_edit_image_nano_banana tool disabled (cloudflare-gemini-image unavailable)")
	}
	if _, err := imageProviderRouter.Get("openrouter-image"); err == nil {
		toolRegistry.Register(tool.NewOpenRouterImageGenerate(
			storageSvc,
			ossLinkSvc,
			artifactSvc.CreateImageArtifact,
			artifactSvc.UpdateMetadataByID,
			func(ctx context.Context, userID, conversationID, messageID uuid.UUID, tokenUsage *llm.LlmUsage) {
				if usageSvc == nil || tokenUsage == nil || tokenUsage.TotalTokens() <= 0 {
					return
				}
				usageSvc.RecordAsync(
					ctx,
					userID,
					conversationID,
					messageID,
					cfg.LLM.OpenRouterImageModel,
					tokenUsage,
					"tool_generate_or_edit_image_openrouter",
					"tool",
					0,
				)
			},
			imageProviderRouter,
			cfg.LLM.OpenRouterImageModel,
			cfg.LLM.OpenRouterImagePhones,
		))
	} else {
		slog.Info("generate_or_edit_image_openrouter tool disabled (openrouter-image unavailable)")
	}
	if strings.TrimSpace(cfg.LLM.Seedance.APIKey) != "" {
		toolRegistry.Register(tool.NewSeedanceVideoGenerate(
			storageSvc,
			ossLinkSvc,
			artifactSvc.CreateVideoArtifact,
			cfg.LLM.Seedance,
		))
	} else {
		slog.Info("generate_video_seedance tool disabled (SEEDANCE_API_KEY missing)")
	}
	agentRegistry, err := agent_profile.LoadFromDir("./config/agents")
	if err != nil {
		slog.Error("failed to load agent yaml config", "err", err)
		os.Exit(1)
	}
	agentSvc := agent_profile.NewService(agentRegistry, providerRouter, toolRegistry)

	// Context Assembler
	promptEngine := llm.NewPromptTemplateEngine()
	toolDefs := toolRegistry.BuildToolDefinitions()
	compactor := llm.NewCompactor(providerRouter)
	contextAsm := llm.NewContextAssembler(promptEngine, providerRouter, storageSvc, ossLinkSvc, toolDefs, compactor)

	// Agent Loop
	agentLoop := agent.NewAgentLoop(providerRouter, toolRegistry, compactor)
	agentLoop.SetMcpExecutorBuilder(func(ctx context.Context, userIDStr string, displayNames []string, allowedChannelNames []string) ([]tool.Tool, error) {
		uid, err := uuid.Parse(userIDStr)
		if err != nil {
			return nil, err
		}
		mcpTools, err := mcpSvc.GetToolsByDisplayNames(ctx, uid, displayNames, allowedChannelNames)
		if err != nil {
			return nil, err
		}
		var executors []tool.Tool
		for _, mt := range mcpTools {
			serverCfg, err := mcpSvc.GetServerConfigForChannel(ctx, mt.ChannelID)
			if err != nil {
				slog.Warn("skip MCP tool, no server config", "tool", mt.DisplayName, "err", err)
				continue
			}
			executors = append(executors, mcp.NewToolExecutor(mt, mcpClient, serverCfg))
		}
		return executors, nil
	})

	// Chat Service
	chatSvc := chat.NewService(providerRouter, contextAsm, toolRegistry, agentLoop, convSvc, agentSvc, userSvc, memorySvc, usageSvc)
	chatSvc.SetTaskScheduler(taskScheduler)

	// Feishu IM Bot
	feishuAdapter := channel.NewFeishuAdapter()
	feishuHandler := channel.NewFeishuHandler(pool, feishuAdapter, chatSvc)
	channelMgr.SetFeishuHandler(feishuHandler, cfg.Feishu.Enabled)
	if cfg.Feishu.Enabled {
		go channelMgr.RestoreFeishuConnections()
	}

	// Memory extraction scheduler
	memExtractor := memory.NewExtractor(providerRouter, memorySvc)
	memScheduler := memory.NewExtractionScheduler(pool, memExtractor)
	maintScheduler := maintenance.NewMaintenanceScheduler(pool, cfg)
	maintScheduler.SetChannelManager(channelMgr)

	// Start background services
	go taskScheduler.Start(ctx)
	go memScheduler.Start(ctx)
	go maintScheduler.Start(ctx)

	// Handlers
	app := &api.App{
		Cfg:    cfg,
		Pool:   pool,
		JwtSvc: jwtSvc,

		AuthHandler:     api.NewAuthHandler(authSvc),
		ChatHandler:     api.NewChatHandler(chatSvc),
		ConvHandler:     api.NewConversationHandler(convSvc),
		ArtifactHandler: api.NewArtifactHandler(artifactSvc, storageSvc, stsProvider),
		MemoryHandler:   api.NewMemoryHandler(memorySvc),
		UserHandler:     api.NewUserHandler(userSvc),
		FeedbackHandler: api.NewFeedbackHandler(feedbackSvc),
		UsageHandler:    api.NewUsageHandler(usageSvc),
		AdminHandler:    api.NewAdminHandler(adminSvc),
		AgentHandler:    api.NewAgentHandler(agentSvc),
		SkillHandler:    api.NewSkillHandler(skillRegistry),
		TaskHandler:     api.NewTaskHandler(taskScheduler, taskExecutor),
		ChannelHandler:  api.NewChannelHandler(channelMgr, oauthSvc),
		DebugHandler:    api.NewDebugHandler(authSvc, cfg.DebugEnabled),
	}

	handler := api.NewRouter(app)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	errCh := make(chan error, 1)
	go func() {
		slog.Info("server listening", "addr", srv.Addr)
		errCh <- srv.ListenAndServe()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		slog.Error("server error", "err", err)
	case sig := <-quit:
		slog.Info("shutting down", "signal", sig)
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}

	slog.Info("server stopped")
}
