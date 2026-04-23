package api

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liteflow/backend/internal/auth"
	"github.com/liteflow/backend/internal/config"
)

type App struct {
	Cfg             config.Config
	Pool            *pgxpool.Pool
	JwtSvc          *auth.JwtService
	AuthHandler     *AuthHandler
	ChatHandler     *ChatHandler
	ConvHandler     *ConversationHandler
	ArtifactHandler *ArtifactHandler
	MemoryHandler   *MemoryHandler
	UserHandler     *UserHandler
	FeedbackHandler *FeedbackHandler
	UsageHandler    *UsageHandler
	AdminHandler    *AdminHandler
	AgentHandler    *AgentHandler
	SkillHandler    *SkillHandler
	TaskHandler     *TaskHandler
	ChannelHandler  *ChannelHandler
	DebugHandler    *DebugHandler
}

func NewRouter(app *App) http.Handler {
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /api/health", app.DebugHandler.HealthCheck)

	// Auth (public)
	mux.HandleFunc("POST /api/auth/send-code", app.AuthHandler.SendCode)
	mux.HandleFunc("POST /api/auth/login", app.AuthHandler.Login)
	mux.HandleFunc("POST /api/auth/refresh", app.AuthHandler.Refresh)
	mux.HandleFunc("POST /api/auth/logout", app.AuthHandler.Logout)

	// Debug (public)
	mux.HandleFunc("GET /api/debug/latest-code", app.DebugHandler.GetLatestCode)

	// Chat
	mux.HandleFunc("POST /api/chat/stream", app.ChatHandler.Stream)
	mux.HandleFunc("POST /api/chat/regenerate", app.ChatHandler.Regenerate)

	// Conversations
	mux.HandleFunc("GET /api/conversations", app.ConvHandler.List)
	mux.HandleFunc("POST /api/conversations", app.ConvHandler.Create)
	mux.HandleFunc("GET /api/conversations/{id}/messages", app.ConvHandler.GetMessages)
	mux.HandleFunc("PUT /api/conversations/{id}", app.ConvHandler.UpdateTitle)
	mux.HandleFunc("PUT /api/conversations/{id}/title", app.ConvHandler.UpdateTitle)
	mux.HandleFunc("POST /api/conversations/{id}/archive", app.ConvHandler.Archive)
	mux.HandleFunc("POST /api/conversations/{id}/unarchive", app.ConvHandler.Unarchive)
	mux.HandleFunc("POST /api/conversations/batch", app.ConvHandler.Batch)
	mux.HandleFunc("DELETE /api/conversations/{id}", app.ConvHandler.Delete)
	mux.HandleFunc("DELETE /api/conversations/{id}/messages", app.ConvHandler.ClearMessages)

	// Conversation files (loaded from artifacts)
	mux.HandleFunc("GET /api/conversations/{id}/files", app.ArtifactHandler.ListFiles)
	mux.HandleFunc("POST /api/conversations/{id}/files/sts", app.ArtifactHandler.GetUploadSTS)
	mux.HandleFunc("POST /api/conversations/{id}/files", app.ArtifactHandler.CompleteUpload)
	mux.HandleFunc("POST /api/conversations/{id}/files/complete", app.ArtifactHandler.CompleteUpload)
	mux.HandleFunc("GET /api/conversations/{id}/files/download", app.ArtifactHandler.DownloadFile)
	mux.HandleFunc("GET /api/conversations/{id}/files/thumbnail-url", app.ArtifactHandler.GetThumbnailURL)
	mux.HandleFunc("DELETE /api/conversations/{id}/files/delete", app.ArtifactHandler.DeleteFile)

	// Artifacts
	mux.HandleFunc("GET /api/conversations/{conversationId}/artifacts", app.ArtifactHandler.GetByConversation)
	mux.HandleFunc("GET /api/artifacts/{id}", app.ArtifactHandler.GetByID)
	mux.HandleFunc("GET /api/artifacts/{id}/versions", app.ArtifactHandler.GetVersionHistory)
	mux.HandleFunc("DELETE /api/artifacts/{id}", app.ArtifactHandler.Delete)

	// Memories
	mux.HandleFunc("GET /api/memories", app.MemoryHandler.List)
	mux.HandleFunc("POST /api/memories", app.MemoryHandler.Add)
	mux.HandleFunc("PUT /api/memories/{id}", app.MemoryHandler.Update)
	mux.HandleFunc("DELETE /api/memories/{id}", app.MemoryHandler.Delete)

	// User
	mux.HandleFunc("GET /api/user/profile", app.UserHandler.GetProfile)
	mux.HandleFunc("PUT /api/user/name", app.UserHandler.UpdateName)
	mux.HandleFunc("GET /api/user/settings", app.UserHandler.GetSettings)
	mux.HandleFunc("PUT /api/user/settings", app.UserHandler.UpdateSettings)

	// User Channels (matches Java: /api/user/channels)
	mux.HandleFunc("GET /api/user/channels", app.ChannelHandler.List)
	mux.HandleFunc("POST /api/user/channels", app.ChannelHandler.Add)
	mux.HandleFunc("GET /api/user/channels/{id}", app.ChannelHandler.Get)
	mux.HandleFunc("PUT /api/user/channels/{id}", app.ChannelHandler.Update)
	mux.HandleFunc("DELETE /api/user/channels/{id}", app.ChannelHandler.Delete)
	mux.HandleFunc("POST /api/user/channels/{id}/refresh", app.ChannelHandler.Refresh)
	mux.HandleFunc("GET /api/user/channels/{id}/tools", app.ChannelHandler.GetTools)
	mux.HandleFunc("POST /api/user/channels/oauth/authorize", app.ChannelHandler.OAuthAuthorize)
	mux.HandleFunc("POST /api/user/channels/oauth/callback", app.ChannelHandler.OAuthCallback)

	// Feedback
	mux.HandleFunc("POST /api/feedbacks", app.FeedbackHandler.Submit)
	mux.HandleFunc("GET /api/feedbacks/{messageId}", app.FeedbackHandler.GetByMessage)

	// Usage
	mux.HandleFunc("GET /api/user/usage", app.UsageHandler.GetUserUsage)
	mux.HandleFunc("GET /api/usage/daily", app.UsageHandler.GetDailySummary)
	mux.HandleFunc("GET /api/usage/history", app.UsageHandler.GetHistory)

	// Skills
	mux.HandleFunc("GET /api/skills", app.SkillHandler.List)
	mux.HandleFunc("GET /api/skills/search", app.SkillHandler.Search)

	// Agents
	mux.HandleFunc("GET /api/agents", app.AgentHandler.List)
	mux.HandleFunc("GET /api/agents/main", app.AgentHandler.GetMain)

	// Tasks
	mux.HandleFunc("GET /api/tasks", app.TaskHandler.List)
	mux.HandleFunc("POST /api/tasks", app.TaskHandler.Create)
	mux.HandleFunc("GET /api/tasks/{id}", app.TaskHandler.GetByID)
	mux.HandleFunc("PUT /api/tasks/{id}", app.TaskHandler.Update)
	mux.HandleFunc("DELETE /api/tasks/{id}", app.TaskHandler.Delete)
	mux.HandleFunc("PATCH /api/tasks/{id}/status", app.TaskHandler.UpdateStatus)
	mux.HandleFunc("POST /api/tasks/{id}/pause", app.TaskHandler.Pause)
	mux.HandleFunc("POST /api/tasks/{id}/resume", app.TaskHandler.Resume)
	mux.HandleFunc("POST /api/tasks/{id}/run", app.TaskHandler.RunNow)
	mux.HandleFunc("GET /api/tasks/{id}/executions", app.TaskHandler.GetExecutions)

	// Channels
	mux.HandleFunc("GET /api/channels", app.ChannelHandler.List)
	mux.HandleFunc("GET /api/channels/{id}", app.ChannelHandler.Get)
	mux.HandleFunc("DELETE /api/channels/{id}", app.ChannelHandler.Delete)

	// Admin
	mux.HandleFunc("GET /api/admin/users", app.AdminHandler.ListUsers)
	mux.HandleFunc("GET /api/admin/stats", app.AdminHandler.GetStats)
	mux.HandleFunc("POST /api/admin/set-admin", app.AdminHandler.SetAdmin)
	mux.HandleFunc("DELETE /api/admin/users/{id}", app.AdminHandler.DeleteUser)

	return Chain(mux,
		RecoveryMiddleware(),
		CORSMiddleware(app.Cfg.AllowedOrigins),
		TraceIDMiddleware(),
		LoggingMiddleware(),
		auth.JWTAuthMiddleware(app.JwtSvc, app.Pool),
	)
}
