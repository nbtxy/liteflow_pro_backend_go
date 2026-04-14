package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/liteflow/backend/internal/admin"
	"github.com/liteflow/backend/internal/agent"
	"github.com/liteflow/backend/internal/api"
	"github.com/liteflow/backend/internal/artifact"
	"github.com/liteflow/backend/internal/auth"
	"github.com/liteflow/backend/internal/channel"
	"github.com/liteflow/backend/internal/chat"
	"github.com/liteflow/backend/internal/config"
	"github.com/liteflow/backend/internal/conversation"
	"github.com/liteflow/backend/internal/feedback"
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

	// Services
	storageSvc := storage.NewLocal(cfg.Storage.BasePath)
	convSvc := conversation.NewService(pool, storageSvc)
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
	var ossLinkSvc storage.Service
	if cfg.Storage.AliyunEndpoint != "" &&
		cfg.Storage.AliyunBucket != "" &&
		cfg.Storage.AliyunKeyID != "" &&
		cfg.Storage.AliyunSecret != "" {
		ossSvc, err := storage.NewOSS(
			cfg.Storage.AliyunEndpoint,
			cfg.Storage.AliyunBucket,
			cfg.Storage.AliyunKeyID,
			cfg.Storage.AliyunSecret,
		)
		if err != nil {
			slog.Error("failed to initialize OSS link storage", "err", err)
			os.Exit(1)
		}
		ossLinkSvc = ossSvc
		slog.Info("OSS link storage enabled for LLM attachments")
	}
	memorySvc := memory.NewService(pool)
	feedbackSvc := feedback.NewService(pool)
	userSvc := user.NewService(pool)
	adminSvc := admin.NewService(pool)
	mcpClient := mcp.NewClient()
	mcpSvc := mcp.NewService(pool)
	channelMgr := channel.NewManager(pool, mcpClient)
	oauthSvc := mcp.NewOAuthService(cfg.MCP)
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
	toolRegistry.Register(tool.NewCurrentTime())
	toolRegistry.Register(tool.NewWebSearch(searchRouter))
	toolRegistry.Register(tool.NewActiveMCP(mcpSvc.ActivateByPlatform))
	toolRegistry.Register(tool.NewMemoryManage(memorySvc.ManageTool))
	toolRegistry.Register(tool.NewSearchSkill(skillRegistry.Search))
	toolRegistry.Register(tool.NewManageScheduledTask(taskScheduler.ManageTool))
	toolRegistry.Register(tool.NewCreateFile(storageSvc, artifactSvc.CreateFileArtifact))
	toolRegistry.Register(tool.NewEditFile(storageSvc, artifactSvc.CreateFileArtifact))
	toolRegistry.Register(tool.NewReadFile(storageSvc))
	toolRegistry.Register(tool.NewListFiles(artifactSvc.GetLatestArtifacts))
	toolRegistry.Register(tool.NewDeleteFile(artifactSvc.DeleteByPath))
	toolRegistry.Register(tool.NewHttpRequest())

	// Context Assembler
	promptEngine := llm.NewPromptTemplateEngine()
	toolDefs := toolRegistry.BuildToolDefinitions()
	contextAsm := llm.NewContextAssembler(promptEngine, providerRouter, storageSvc, ossLinkSvc, toolDefs)

	// Agent Loop
	agentLoop := agent.NewAgentLoop(providerRouter, toolRegistry)
	agentLoop.SetMcpExecutorBuilder(func(ctx context.Context, userIDStr string, displayNames []string) ([]tool.Tool, error) {
		uid, err := uuid.Parse(userIDStr)
		if err != nil {
			return nil, err
		}
		mcpTools, err := mcpSvc.GetToolsByDisplayNames(ctx, uid, displayNames)
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
	chatSvc := chat.NewService(providerRouter, contextAsm, toolRegistry, agentLoop, convSvc, usageSvc)
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
		ArtifactHandler: api.NewArtifactHandler(artifactSvc, storageSvc),
		MemoryHandler:   api.NewMemoryHandler(memorySvc),
		UserHandler:     api.NewUserHandler(userSvc),
		FeedbackHandler: api.NewFeedbackHandler(feedbackSvc),
		UsageHandler:    api.NewUsageHandler(usageSvc),
		AdminHandler:    api.NewAdminHandler(adminSvc),
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
