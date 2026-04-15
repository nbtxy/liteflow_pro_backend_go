package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liteflow/backend/internal/domain"
)

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) GetUserTools(ctx context.Context, userID uuid.UUID) ([]domain.McpTool, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT t.id, t.channel_id, t.user_id, t.tool_name, t.display_name,
		        t.description, t.input_schema, t.category, t.updated_at,
		        COALESCE(c.name, '') as channel_name
		 FROM mcp_tools t
		 LEFT JOIN user_channels c ON t.channel_id = c.id
		 WHERE t.user_id = $1
		 ORDER BY t.tool_name`, userID)
	if err != nil {
		return nil, fmt.Errorf("get user tools: %w", err)
	}
	defer rows.Close()

	var tools []domain.McpTool
	for rows.Next() {
		var t domain.McpTool
		if err := rows.Scan(&t.ID, &t.ChannelID, &t.UserID, &t.ToolName, &t.DisplayName,
			&t.Description, &t.InputSchema, &t.Category, &t.UpdatedAt, &t.ChannelName); err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, nil
}

func (s *Service) BuildActiveMcpToolDescription(ctx context.Context, userID uuid.UUID) (string, error) {
	tools, err := s.GetUserTools(ctx, userID)
	if err != nil {
		return "", err
	}

	if len(tools) == 0 {
		return "", nil
	}

	var sb strings.Builder
	sb.WriteString("Available MCP tools that can be activated:\n")
	for _, t := range tools {
		sb.WriteString(fmt.Sprintf("- %s (%s): %s\n", t.ToolName, t.ChannelName, t.Description))
	}

	return sb.String(), nil
}

func (s *Service) ActivateByPlatform(ctx context.Context, userID fmt.Stringer, platform string) ([]string, error) {
	uid, err := uuid.Parse(userID.String())
	if err != nil {
		return nil, fmt.Errorf("invalid user ID")
	}

	allTools, err := s.GetUserTools(ctx, uid)
	if err != nil {
		return nil, err
	}

	normalizedPlatform := strings.ToLower(strings.TrimSpace(platform))

	var activated []string
	for _, t := range allTools {
		channelName := strings.ToLower(t.ChannelName)
		if strings.Contains(channelName, normalizedPlatform) {
			activated = append(activated, t.DisplayName)
		}
	}

	slog.Info("MCP tools activated by platform", "userId", uid, "platform", platform, "count", len(activated))
	return activated, nil
}

// GetToolsByDisplayNames returns McpTool records for the given display names.
func (s *Service) GetToolsByDisplayNames(ctx context.Context, userID uuid.UUID, displayNames []string) ([]domain.McpTool, error) {
	allTools, err := s.GetUserTools(ctx, userID)
	if err != nil {
		return nil, err
	}

	nameSet := make(map[string]bool, len(displayNames))
	for _, n := range displayNames {
		nameSet[n] = true
	}

	var matched []domain.McpTool
	for _, t := range allTools {
		if nameSet[t.DisplayName] {
			matched = append(matched, t)
		}
	}
	return matched, nil
}

// GetServerConfigForChannel retrieves the server URL and token for a channel.
func (s *Service) GetServerConfigForChannel(ctx context.Context, channelID uuid.UUID) (ServerConfig, error) {
	var configJSON []byte
	err := s.pool.QueryRow(ctx,
		`SELECT config FROM user_channels WHERE id = $1`, channelID).Scan(&configJSON)
	if err != nil {
		return ServerConfig{}, fmt.Errorf("channel not found: %w", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return ServerConfig{}, err
	}

	serverURL, _ := cfg["serverUrl"].(string)
	token, _ := cfg["token"].(string)
	refreshToken, _ := cfg["refreshToken"].(string)
	if refreshToken == "" {
		refreshToken, _ = cfg["refresh_token"].(string)
	}
	provider, _ := cfg["provider"].(string)
	expiresAt, _ := cfg["expiresAt"].(string)
	oauthClientID, _ := cfg["oauthClientId"].(string)
	oauthClientSecret, _ := cfg["oauthClientSecret"].(string)

	return ServerConfig{
		ChannelID:         channelID,
		ServerURL:         serverURL,
		Token:             token,
		RefreshToken:      refreshToken,
		Provider:          provider,
		TokenExpiresAt:    expiresAt,
		OAuthClientID:     oauthClientID,
		OAuthClientSecret: oauthClientSecret,
	}, nil
}

func (s *Service) RefreshChannelToken(ctx context.Context, cfg ServerConfig, oauthSvc *OAuthService) (ServerConfig, error) {
	if oauthSvc == nil {
		return ServerConfig{}, fmt.Errorf("oauth service not available")
	}
	if cfg.Provider == "" {
		return ServerConfig{}, fmt.Errorf("缺少 provider，无法刷新 token")
	}
	if cfg.RefreshToken == "" {
		return ServerConfig{}, fmt.Errorf("缺少 refresh_token，无法刷新 token")
	}

	tokenSet, err := oauthSvc.RefreshAccessToken(ctx, cfg.Provider, cfg.RefreshToken, cfg.OAuthClientID, cfg.OAuthClientSecret)
	if err != nil {
		return ServerConfig{}, err
	}

	updated := cfg
	updated.Token = tokenSet.AccessToken
	if tokenSet.RefreshToken != "" {
		updated.RefreshToken = tokenSet.RefreshToken
	}
	if tokenSet.ExpiresIn > 0 {
		updated.TokenExpiresAt = time.Now().Add(time.Duration(tokenSet.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}

	if cfg.ChannelID == uuid.Nil {
		return updated, nil
	}

	var configJSON []byte
	if err := s.pool.QueryRow(ctx, `SELECT config FROM user_channels WHERE id = $1`, cfg.ChannelID).Scan(&configJSON); err != nil {
		return ServerConfig{}, fmt.Errorf("load channel config: %w", err)
	}

	var channelCfg map[string]any
	if err := json.Unmarshal(configJSON, &channelCfg); err != nil {
		return ServerConfig{}, fmt.Errorf("parse channel config: %w", err)
	}

	channelCfg["token"] = updated.Token
	channelCfg["provider"] = updated.Provider
	if updated.OAuthClientID != "" {
		channelCfg["oauthClientId"] = updated.OAuthClientID
	}
	if updated.OAuthClientSecret != "" {
		channelCfg["oauthClientSecret"] = updated.OAuthClientSecret
	}
	if updated.RefreshToken != "" {
		channelCfg["refreshToken"] = updated.RefreshToken
	}
	if updated.TokenExpiresAt != "" {
		channelCfg["expiresAt"] = updated.TokenExpiresAt
	}

	newConfigJSON, err := json.Marshal(channelCfg)
	if err != nil {
		return ServerConfig{}, fmt.Errorf("marshal channel config: %w", err)
	}

	if _, err := s.pool.Exec(ctx,
		`UPDATE user_channels SET config = $1, updated_at = $2 WHERE id = $3`,
		newConfigJSON, time.Now(), cfg.ChannelID); err != nil {
		return ServerConfig{}, fmt.Errorf("persist refreshed token: %w", err)
	}

	slog.Info("MCP token refreshed", "channelId", cfg.ChannelID, "provider", cfg.Provider)
	return updated, nil
}
