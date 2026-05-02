package api

import (
	"net/http"
	"strings"

	"github.com/liteflow/backend/internal/agent"
	"github.com/liteflow/backend/internal/auth"
	"github.com/liteflow/backend/internal/chat"
)

type ChatHandler struct {
	chatSvc *chat.Service
}

func NewChatHandler(chatSvc *chat.Service) *ChatHandler {
	return &ChatHandler{chatSvc: chatSvc}
}

func (h *ChatHandler) Stream(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	var req chat.ChatRequest
	if err := DecodeJSON(r, &req); err != nil {
		BadRequest(w, "invalid request body")
		return
	}

	if !req.Reconnect && strings.TrimSpace(req.Message) == "" {
		BadRequest(w, "message is required")
		return
	}
	if req.Reconnect && strings.TrimSpace(req.StreamID) == "" {
		BadRequest(w, "streamId is required for reconnect")
		return
	}

	sse, err := agent.NewSSEWriter(w)
	if err != nil {
		InternalError(w, "streaming not supported")
		return
	}

	events, _, err := h.chatSvc.OpenStream(req, userID)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}
	sse.SendEvents(events)
}

func (h *ChatHandler) StopStream(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	var body struct {
		StreamID string `json:"streamId"`
	}
	if err := DecodeJSON(r, &body); err != nil || strings.TrimSpace(body.StreamID) == "" {
		BadRequest(w, "streamId is required")
		return
	}

	if ok := h.chatSvc.StopStream(body.StreamID, userID); !ok {
		NotFound(w, "stream not found")
		return
	}
	OK(w, map[string]any{"stopped": true})
}

func (h *ChatHandler) Regenerate(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	var req chat.RegenerateRequest
	if err := DecodeJSON(r, &req); err != nil {
		BadRequest(w, "invalid request body")
		return
	}
	if !req.Reconnect && (strings.TrimSpace(req.ConversationID) == "" || strings.TrimSpace(req.MessageID) == "") {
		BadRequest(w, "conversationId and messageId are required")
		return
	}
	if req.Reconnect && strings.TrimSpace(req.StreamID) == "" {
		BadRequest(w, "streamId is required for reconnect")
		return
	}

	sse, err := agent.NewSSEWriter(w)
	if err != nil {
		InternalError(w, "streaming not supported")
		return
	}

	events, _, err := h.chatSvc.OpenRegenerateStream(req, userID)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}
	sse.SendEvents(events)
}
