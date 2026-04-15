package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"github.com/liteflow/backend/internal/domain"
	"github.com/liteflow/backend/internal/mcp"
)

type managedConnection struct {
	channelID uuid.UUID
	wsClient  *larkws.Client
	apiClient *lark.Client
}

type Manager struct {
	pool          *pgxpool.Pool
	mcpClient     *mcp.Client
	feishuHandler *FeishuHandler
	feishuEnabled bool
	activeConns   map[uuid.UUID]*managedConnection
	activeConnsMu sync.RWMutex
}

func NewManager(pool *pgxpool.Pool, mcpClient *mcp.Client) *Manager {
	return &Manager{
		pool:        pool,
		mcpClient:   mcpClient,
		activeConns: make(map[uuid.UUID]*managedConnection),
	}
}

// SetFeishuHandler sets the Feishu handler and enables Feishu support.
func (m *Manager) SetFeishuHandler(h *FeishuHandler, enabled bool) {
	m.feishuHandler = h
	m.feishuEnabled = enabled
	if h != nil {
		h.SetManager(m)
	}
}

// RestoreFeishuConnections restores all active Feishu connections on startup.
func (m *Manager) RestoreFeishuConnections() {
	if !m.feishuEnabled || m.feishuHandler == nil {
		return
	}

	ctx := context.Background()
	rows, err := m.pool.Query(ctx,
		`SELECT id, user_id, type, name, display_name, config, status, error_message, authorized_account_id, created_at, updated_at
		 FROM user_channels WHERE type = 'im' AND name = 'feishu' AND status = 'active'`)
	if err != nil {
		slog.Warn("failed to query feishu channels for restore", "err", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var ch domain.UserChannel
		if err := rows.Scan(&ch.ID, &ch.UserID, &ch.Type, &ch.Name, &ch.DisplayName,
			&ch.Config, &ch.Status, &ch.ErrorMessage, &ch.AuthorizedAccountID,
			&ch.CreatedAt, &ch.UpdatedAt); err != nil {
			slog.Warn("scan feishu channel failed", "err", err)
			continue
		}
		if err := m.startFeishuConnection(&ch); err != nil {
			slog.Error("restore feishu connection failed", "channelId", ch.ID, "err", err)
			errMsg := "启动恢复失败: " + err.Error()
			m.UpdateStatus(ctx, ch.ID, "error", &errMsg)
		} else {
			count++
			slog.Info("restored feishu connection", "channelId", ch.ID)
		}
	}

	slog.Info("restored feishu connections", "count", count)
}

func (m *Manager) startFeishuConnection(ch *domain.UserChannel) error {
	var cfg map[string]any
	if err := json.Unmarshal(ch.Config, &cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	appID, _ := cfg["appId"].(string)
	appSecret, _ := cfg["appSecret"].(string)
	if appID == "" || appSecret == "" {
		return fmt.Errorf("missing appId or appSecret")
	}

	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			go m.feishuHandler.HandleEvent(ch, event)
			return nil
		})

	wsClient := larkws.NewClient(appID, appSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithAutoReconnect(true),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
	)

	go func() {
		if err := wsClient.Start(context.Background()); err != nil {
			slog.Error("feishu websocket start failed", "channelId", ch.ID, "err", err)
		}
	}()

	apiClient := lark.NewClient(appID, appSecret)

	m.activeConnsMu.Lock()
	m.activeConns[ch.ID] = &managedConnection{
		channelID: ch.ID,
		wsClient:  wsClient,
		apiClient: apiClient,
	}
	m.activeConnsMu.Unlock()

	return nil
}

// GetLarkClient returns the Lark API client for a channel.
func (m *Manager) GetLarkClient(channelID uuid.UUID) *lark.Client {
	m.activeConnsMu.RLock()
	defer m.activeConnsMu.RUnlock()
	if conn, ok := m.activeConns[channelID]; ok {
		return conn.apiClient
	}
	return nil
}

// IsFeishuConnected checks if a Feishu channel has an active WebSocket connection.
func (m *Manager) IsFeishuConnected(channelID uuid.UUID) bool {
	m.activeConnsMu.RLock()
	defer m.activeConnsMu.RUnlock()
	_, ok := m.activeConns[channelID]
	return ok
}

// HealthCheck verifies active Feishu channels have live in-memory connections.
// If a connection is missing, it attempts to reconnect.
func (m *Manager) HealthCheck(ctx context.Context) {
	if !m.feishuEnabled || m.feishuHandler == nil {
		return
	}

	rows, err := m.pool.Query(ctx,
		`SELECT id, user_id, type, name, display_name, config, status, error_message, authorized_account_id, created_at, updated_at
		 FROM user_channels WHERE type = 'im' AND name = 'feishu' AND status = 'active'`)
	if err != nil {
		slog.Warn("feishu health check query failed", "err", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var ch domain.UserChannel
		if err := rows.Scan(&ch.ID, &ch.UserID, &ch.Type, &ch.Name, &ch.DisplayName,
			&ch.Config, &ch.Status, &ch.ErrorMessage, &ch.AuthorizedAccountID,
			&ch.CreatedAt, &ch.UpdatedAt); err != nil {
			slog.Warn("feishu health check scan failed", "err", err)
			continue
		}
		if m.IsFeishuConnected(ch.ID) {
			continue
		}
		if err := m.startFeishuConnection(&ch); err != nil {
			slog.Error("feishu health check reconnect failed", "channelId", ch.ID, "err", err)
		} else {
			slog.Info("feishu health check reconnected", "channelId", ch.ID)
		}
	}
}

func (m *Manager) Create(ctx context.Context, ch *domain.UserChannel) error {
	if ch.ID == uuid.Nil {
		ch.ID = uuid.New()
	}
	now := time.Now()
	ch.CreatedAt = now
	ch.UpdatedAt = now

	_, err := m.pool.Exec(ctx,
		`INSERT INTO user_channels (id, user_id, type, name, display_name, config, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		ch.ID, ch.UserID, ch.Type, ch.Name, ch.DisplayName, ch.Config, ch.Status, ch.CreatedAt, ch.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create channel: %w", err)
	}

	slog.Info("channel created", "id", ch.ID, "type", ch.Type)
	return nil
}

func (m *Manager) GetByUserID(ctx context.Context, userID uuid.UUID) ([]domain.UserChannel, error) {
	rows, err := m.pool.Query(ctx,
		`SELECT c.id, c.user_id, c.type, c.name, c.display_name, c.config, c.status,
		        c.error_message, c.authorized_account_id, c.created_at, c.updated_at,
		        (SELECT COUNT(*) FROM mcp_tools t WHERE t.channel_id = c.id) as tool_count
		 FROM user_channels c WHERE c.user_id = $1 ORDER BY c.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []domain.UserChannel
	for rows.Next() {
		var ch domain.UserChannel
		if err := rows.Scan(&ch.ID, &ch.UserID, &ch.Type, &ch.Name, &ch.DisplayName,
			&ch.Config, &ch.Status, &ch.ErrorMessage, &ch.AuthorizedAccountID,
			&ch.CreatedAt, &ch.UpdatedAt, &ch.ToolCount); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, nil
}

func (m *Manager) GetByID(ctx context.Context, id uuid.UUID) (*domain.UserChannel, error) {
	var ch domain.UserChannel
	err := m.pool.QueryRow(ctx,
		`SELECT id, user_id, type, name, display_name, config, status,
		        error_message, authorized_account_id, created_at, updated_at
		 FROM user_channels WHERE id = $1`, id).Scan(
		&ch.ID, &ch.UserID, &ch.Type, &ch.Name, &ch.DisplayName,
		&ch.Config, &ch.Status, &ch.ErrorMessage, &ch.AuthorizedAccountID,
		&ch.CreatedAt, &ch.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

func (m *Manager) UpdateStatus(ctx context.Context, id uuid.UUID, status string, errMsg *string) error {
	_, err := m.pool.Exec(ctx,
		`UPDATE user_channels SET status = $1, error_message = $2, updated_at = $3 WHERE id = $4`,
		status, errMsg, time.Now(), id)
	return err
}

func (m *Manager) Delete(ctx context.Context, id, userID uuid.UUID) error {
	// Disconnect Feishu WebSocket if active
	m.activeConnsMu.Lock()
	delete(m.activeConns, id)
	m.activeConnsMu.Unlock()

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tx.Exec(ctx, `DELETE FROM mcp_tools WHERE channel_id = $1`, id)
	tx.Exec(ctx, `DELETE FROM channel_user_mappings WHERE channel_id = $1`, id)
	_, err = tx.Exec(ctx, `DELETE FROM user_channels WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (m *Manager) AddChannel(ctx context.Context, userID uuid.UUID, chType, name, displayName string, config map[string]any) (*domain.UserChannel, error) {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	ch := &domain.UserChannel{
		ID:          uuid.New(),
		UserID:      userID,
		Type:        chType,
		Name:        &name,
		DisplayName: &displayName,
		Config:      configJSON,
		Status:      "pending",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := m.Create(ctx, ch); err != nil {
		return nil, err
	}

	switch chType {
	case "mcp":
		toolCount, err := m.discoverAndSaveTools(ctx, ch.ID, userID, displayName, config)
		if err != nil {
			errMsg := err.Error()
			m.UpdateStatus(ctx, ch.ID, "error", &errMsg)
			slog.Warn("MCP tool discovery failed", "channelId", ch.ID, "err", err)
			ch.Status = "error"
			ch.ErrorMessage = &errMsg
		} else {
			m.UpdateStatus(ctx, ch.ID, "active", nil)
			ch.Status = "active"
			tc := toolCount
			ch.ToolCount = &tc
		}
	case "im":
		if m.feishuEnabled && m.feishuHandler != nil {
			if err := m.startFeishuConnection(ch); err != nil {
				errMsg := "连接启动失败: " + err.Error()
				m.UpdateStatus(ctx, ch.ID, "error", &errMsg)
				ch.Status = "error"
				ch.ErrorMessage = &errMsg
			} else {
				m.UpdateStatus(ctx, ch.ID, "active", nil)
				ch.Status = "active"
			}
		} else {
			m.UpdateStatus(ctx, ch.ID, "active", nil)
			ch.Status = "active"
		}
	}

	slog.Info("channel added", "id", ch.ID, "type", chType, "name", name, "status", ch.Status)
	return ch, nil
}

func (m *Manager) UpdateChannel(ctx context.Context, id, userID uuid.UUID, name, displayName string, config map[string]any) (*domain.UserChannel, error) {
	ch, err := m.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("channel not found")
	}
	if ch.UserID != userID {
		return nil, fmt.Errorf("channel not found")
	}

	if name != "" {
		ch.Name = &name
	}
	if displayName != "" {
		ch.DisplayName = &displayName
	}
	if config != nil {
		configJSON, err := json.Marshal(config)
		if err != nil {
			return nil, fmt.Errorf("marshal config: %w", err)
		}
		ch.Config = configJSON
	}
	ch.Status = "pending"
	ch.UpdatedAt = time.Now()

	_, err = m.pool.Exec(ctx,
		`UPDATE user_channels SET name=$1, display_name=$2, config=$3, status=$4, updated_at=$5 WHERE id=$6`,
		ch.Name, ch.DisplayName, ch.Config, ch.Status, ch.UpdatedAt, ch.ID)
	if err != nil {
		return nil, fmt.Errorf("update channel: %w", err)
	}

	switch ch.Type {
	case "mcp":
		var cfgMap map[string]any
		json.Unmarshal(ch.Config, &cfgMap)
		dn := ""
		if ch.DisplayName != nil {
			dn = *ch.DisplayName
		}
		if _, err := m.discoverAndSaveTools(ctx, ch.ID, ch.UserID, dn, cfgMap); err != nil {
			errMsg := err.Error()
			m.UpdateStatus(ctx, ch.ID, "error", &errMsg)
		} else {
			m.UpdateStatus(ctx, ch.ID, "active", nil)
		}
	case "im":
		// Restart Feishu connection with new config
		m.activeConnsMu.Lock()
		delete(m.activeConns, ch.ID)
		m.activeConnsMu.Unlock()
		if m.feishuEnabled && m.feishuHandler != nil {
			if err := m.startFeishuConnection(ch); err != nil {
				errMsg := "重连失败: " + err.Error()
				m.UpdateStatus(ctx, ch.ID, "error", &errMsg)
			} else {
				m.UpdateStatus(ctx, ch.ID, "active", nil)
			}
		} else {
			m.UpdateStatus(ctx, ch.ID, "active", nil)
		}
	}

	return m.GetByID(ctx, ch.ID)
}

func (m *Manager) RefreshTools(ctx context.Context, id, userID uuid.UUID) (*domain.UserChannel, error) {
	ch, err := m.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("channel not found")
	}
	if ch.UserID != userID {
		return nil, fmt.Errorf("channel not found")
	}
	if ch.Type != "mcp" {
		return nil, fmt.Errorf("仅 MCP 类型渠道支持刷新操作")
	}

	var cfgMap map[string]any
	json.Unmarshal(ch.Config, &cfgMap)
	dn := ""
	if ch.DisplayName != nil {
		dn = *ch.DisplayName
	}

	if _, err := m.discoverAndSaveTools(ctx, id, userID, dn, cfgMap); err != nil {
		errMsg := err.Error()
		m.UpdateStatus(ctx, id, "error", &errMsg)
		return nil, fmt.Errorf("刷新失败: %w", err)
	}

	m.UpdateStatus(ctx, id, "active", nil)
	return m.GetByID(ctx, id)
}

func (m *Manager) GetChannelTools(ctx context.Context, channelID uuid.UUID) ([]map[string]any, error) {
	rows, err := m.pool.Query(ctx,
		`SELECT tool_name, display_name, description, category FROM mcp_tools WHERE channel_id = $1 ORDER BY tool_name`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tools []map[string]any
	for rows.Next() {
		var toolName, displayName, description, category string
		if err := rows.Scan(&toolName, &displayName, &description, &category); err != nil {
			return nil, err
		}
		tools = append(tools, map[string]any{
			"toolName":    toolName,
			"displayName": displayName,
			"description": description,
			"category":    category,
		})
	}
	if tools == nil {
		tools = []map[string]any{}
	}
	return tools, nil
}

// discoverAndSaveTools connects to the MCP server, lists tools, and saves them to DB.
// Returns the number of tools actually saved.
func (m *Manager) discoverAndSaveTools(ctx context.Context, channelID, userID uuid.UUID, displayName string, config map[string]any) (int, error) {
	serverURL, _ := config["serverUrl"].(string)
	token, _ := config["token"].(string)
	provider, _ := config["provider"].(string)
	refreshToken, _ := config["refreshToken"].(string)
	if refreshToken == "" {
		refreshToken, _ = config["refresh_token"].(string)
	}
	expiresAt, _ := config["expiresAt"].(string)
	oauthClientID, _ := config["oauthClientId"].(string)
	oauthClientSecret, _ := config["oauthClientSecret"].(string)

	if serverURL == "" {
		return 0, fmt.Errorf("serverUrl is required")
	}

	cfg := mcp.ServerConfig{
		ChannelID:         channelID,
		ServerURL:         serverURL,
		Token:             token,
		RefreshToken:      refreshToken,
		Provider:          provider,
		TokenExpiresAt:    expiresAt,
		OAuthClientID:     oauthClientID,
		OAuthClientSecret: oauthClientSecret,
	}
	tools, err := m.mcpClient.ListTools(ctx, cfg)
	if err != nil {
		return 0, fmt.Errorf("无法连接 MCP Server: %w", err)
	}

	slog.Info("MCP tools discovered", "channelId", channelID, "count", len(tools))

	// Delete old tools
	m.pool.Exec(ctx, `DELETE FROM mcp_tools WHERE channel_id = $1`, channelID)

	// Save new tools
	prefix := sanitizeName(strings.ToLower(displayName))
	savedCount := 0
	for _, t := range tools {
		schemaJSON, _ := json.Marshal(t.InputSchema)
		toolDisplayName := prefix + "__" + sanitizeName(t.Name)
		desc := truncateUTF8(t.Description, 200)

		_, err := m.pool.Exec(ctx,
			`INSERT INTO mcp_tools (id, channel_id, user_id, tool_name, display_name, description, input_schema, category, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			uuid.New(), channelID, userID, t.Name, toolDisplayName, desc, schemaJSON, "", time.Now())
		if err != nil {
			slog.Warn("failed to save MCP tool", "tool", t.Name, "err", err)
		} else {
			savedCount++
		}
	}

	return savedCount, nil
}

// truncateUTF8 safely truncates a string to maxRunes runes without breaking multi-byte characters.
func truncateUTF8(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

var nonAlphaNumRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func sanitizeName(name string) string {
	if name == "" {
		return "unknown"
	}
	return nonAlphaNumRe.ReplaceAllString(name, "_")
}
