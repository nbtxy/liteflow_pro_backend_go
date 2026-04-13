package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

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
	return ServerConfig{ServerURL: serverURL, Token: token}, nil
}
