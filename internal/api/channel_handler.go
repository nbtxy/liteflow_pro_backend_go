package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/auth"
	"github.com/liteflow/backend/internal/channel"
	"github.com/liteflow/backend/internal/domain"
	"github.com/liteflow/backend/internal/mcp"
)

type ChannelHandler struct {
	channelMgr *channel.Manager
	oauthSvc   *mcp.OAuthService
}

func NewChannelHandler(channelMgr *channel.Manager, oauthSvc *mcp.OAuthService) *ChannelHandler {
	return &ChannelHandler{channelMgr: channelMgr, oauthSvc: oauthSvc}
}

// toSafeResponse builds a frontend-safe response, hiding secrets and adding derived fields.
func toSafeResponse(ch domain.UserChannel, mgr *channel.Manager) map[string]any {
	resp := map[string]any{
		"id":          ch.ID,
		"type":        ch.Type,
		"name":        ch.Name,
		"displayName": ch.DisplayName,
		"status":      ch.Status,
		"createdAt":   ch.CreatedAt.Format(time.RFC3339),
		"updatedAt":   ch.UpdatedAt.Format(time.RFC3339),
	}

	if ch.ErrorMessage != nil {
		resp["errorMessage"] = *ch.ErrorMessage
	}

	var cfg map[string]any
	if ch.Config != nil {
		json.Unmarshal(ch.Config, &cfg)
	}

	name := ""
	if ch.Name != nil {
		name = *ch.Name
	}

	if ch.Type == "im" && name == "feishu" {
		if cfg != nil {
			if appID, ok := cfg["appId"].(string); ok {
				resp["appId"] = appID
			}
		}
		if mgr != nil {
			resp["connected"] = mgr.IsFeishuConnected(ch.ID)
		} else {
			resp["connected"] = ch.Status == "active"
		}
	} else if ch.Type == "mcp" {
		resp["connected"] = ch.Status == "active"
		if cfg != nil {
			if serverURL, ok := cfg["serverUrl"].(string); ok {
				resp["serverUrl"] = serverURL
			}
			if provider, ok := cfg["provider"].(string); ok {
				resp["provider"] = provider
			}
			if username, ok := cfg["username"].(string); ok {
				resp["username"] = username
			}
			if scopes, ok := cfg["scopes"]; ok {
				resp["scopes"] = scopes
			}
			if perms, ok := cfg["permissions"]; ok {
				resp["permissions"] = perms
			}
		}
		if ch.ToolCount != nil {
			resp["toolCount"] = *ch.ToolCount
		} else {
			resp["toolCount"] = 0
		}
	}

	return resp
}

func (h *ChannelHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	channels, err := h.channelMgr.GetByUserID(r.Context(), userID)
	if err != nil {
		InternalError(w, "failed to list channels")
		return
	}

	typeFilter := r.URL.Query().Get("type")

	result := make([]map[string]any, 0, len(channels))
	for _, ch := range channels {
		if typeFilter != "" && ch.Type != typeFilter {
			continue
		}
		result = append(result, toSafeResponse(ch, h.channelMgr))
	}

	OK(w, result)
}

func (h *ChannelHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid channel id")
		return
	}

	ch, err := h.channelMgr.GetByID(r.Context(), id)
	if err != nil {
		NotFound(w, "channel not found")
		return
	}

	OK(w, toSafeResponse(*ch, h.channelMgr))
}

func (h *ChannelHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid channel id")
		return
	}

	if err := h.channelMgr.Delete(r.Context(), id, userID); err != nil {
		InternalError(w, "failed to delete channel")
		return
	}

	OKEmpty(w)
}

func (h *ChannelHandler) Add(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	var body struct {
		Type        string         `json:"type"`
		Name        string         `json:"name"`
		DisplayName string         `json:"displayName"`
		Config      map[string]any `json:"config"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		BadRequest(w, "invalid request body")
		return
	}
	if body.DisplayName == "" {
		body.DisplayName = body.Name
	}

	// IM Feishu: extract appId/appSecret and store as token format
	if body.Type == "im" && body.Name == "feishu" {
		appID, _ := body.Config["appId"].(string)
		appSecret, _ := body.Config["appSecret"].(string)
		if appID == "" || appSecret == "" {
			BadRequest(w, "appId and appSecret are required")
			return
		}
		body.Config["token"] = appID + ":" + appSecret
	}

	ch, err := h.channelMgr.AddChannel(r.Context(), userID, body.Type, body.Name, body.DisplayName, body.Config)
	if err != nil {
		InternalError(w, "failed to add channel: "+err.Error())
		return
	}

	OK(w, toSafeResponse(*ch, h.channelMgr))
}

func (h *ChannelHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid channel id")
		return
	}

	var body struct {
		Name        string         `json:"name"`
		DisplayName string         `json:"displayName"`
		Config      map[string]any `json:"config"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		BadRequest(w, "invalid request body")
		return
	}

	ch, err := h.channelMgr.UpdateChannel(r.Context(), id, userID, body.Name, body.DisplayName, body.Config)
	if err != nil {
		InternalError(w, "failed to update channel: "+err.Error())
		return
	}

	OK(w, toSafeResponse(*ch, h.channelMgr))
}

func (h *ChannelHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid channel id")
		return
	}

	ch, err := h.channelMgr.RefreshTools(r.Context(), id, userID)
	if err != nil {
		InternalError(w, "failed to refresh channel: "+err.Error())
		return
	}

	OK(w, toSafeResponse(*ch, h.channelMgr))
}

func (h *ChannelHandler) GetTools(w http.ResponseWriter, r *http.Request) {
	_, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid channel id")
		return
	}

	tools, err := h.channelMgr.GetChannelTools(r.Context(), id)
	if err != nil {
		InternalError(w, "failed to get tools")
		return
	}

	OK(w, tools)
}

func (h *ChannelHandler) OAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	var body struct {
		Provider  string `json:"provider"`
		ServerURL string `json:"serverUrl"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		BadRequest(w, "invalid request body")
		return
	}
	if body.Provider == "" {
		BadRequest(w, "provider is required")
		return
	}

	authURL, state, err := h.oauthSvc.GenerateAuthURL(userID, body.Provider, body.ServerURL)
	if err != nil {
		Error(w, http.StatusBadRequest, 40002, err.Error())
		return
	}

	OK(w, map[string]string{
		"authUrl": authURL,
		"state":   state,
	})
}

func (h *ChannelHandler) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	var body struct {
		Provider    string `json:"provider"`
		Code        string `json:"code"`
		State       string `json:"state"`
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		ServerURL   string `json:"serverUrl"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		BadRequest(w, "invalid request body")
		return
	}
	if body.Code == "" || body.State == "" {
		BadRequest(w, "code and state are required")
		return
	}

	token, err := h.oauthSvc.ExchangeCodeWithState(r.Context(), body.Provider, body.Code, body.State)
	if err != nil {
		InternalError(w, "OAuth token exchange failed: "+err.Error())
		return
	}

	displayName := body.DisplayName
	if displayName == "" {
		displayName = body.Name
	}
	if displayName == "" {
		displayName = body.Provider
	}

	config := map[string]any{
		"serverUrl": body.ServerURL,
		"token":     token,
		"provider":  body.Provider,
	}

	ch, err := h.channelMgr.AddChannel(r.Context(), userID, "mcp", body.Provider, displayName, config)
	if err != nil {
		InternalError(w, "failed to create channel: "+err.Error())
		return
	}

	OK(w, toSafeResponse(*ch, h.channelMgr))
}
