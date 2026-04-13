package api

import (
	"net/http"

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

	if req.Message == "" {
		BadRequest(w, "message is required")
		return
	}

	sse, err := agent.NewSSEWriter(w)
	if err != nil {
		InternalError(w, "streaming not supported")
		return
	}

	events := h.chatSvc.ChatStream(r.Context(), req, userID)
	sse.SendEvents(events)
}

func (h *ChatHandler) Regenerate(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	var body struct {
		ConversationID string `json:"conversationId"`
		MessageID      string `json:"messageId"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.ConversationID == "" || body.MessageID == "" {
		BadRequest(w, "conversationId and messageId are required")
		return
	}

	sse, err := agent.NewSSEWriter(w)
	if err != nil {
		InternalError(w, "streaming not supported")
		return
	}

	events := h.chatSvc.Regenerate(r.Context(), body.ConversationID, body.MessageID, userID)
	sse.SendEvents(events)
}
