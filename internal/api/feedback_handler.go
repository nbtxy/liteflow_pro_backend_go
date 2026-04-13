package api

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/auth"
	"github.com/liteflow/backend/internal/domain"
	"github.com/liteflow/backend/internal/feedback"
)

type FeedbackHandler struct {
	feedbackSvc *feedback.Service
}

func NewFeedbackHandler(feedbackSvc *feedback.Service) *FeedbackHandler {
	return &FeedbackHandler{feedbackSvc: feedbackSvc}
}

func (h *FeedbackHandler) Submit(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	var body struct {
		ConversationID string `json:"conversationId"`
		MessageID      string `json:"messageId"`
		Rating         string `json:"rating"`
		Reasons        []string `json:"reasons,omitempty"`
		Comment        *string  `json:"comment,omitempty"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		BadRequest(w, "invalid request body")
		return
	}

	convID, err := uuid.Parse(body.ConversationID)
	if err != nil {
		BadRequest(w, "invalid conversationId")
		return
	}
	msgID, err := uuid.Parse(body.MessageID)
	if err != nil {
		BadRequest(w, "invalid messageId")
		return
	}

	fb := &domain.Feedback{
		UserID:         userID,
		ConversationID: convID,
		MessageID:      msgID,
		Rating:         body.Rating,
		Comment:        body.Comment,
	}

	if err := h.feedbackSvc.Create(r.Context(), fb); err != nil {
		InternalError(w, "failed to submit feedback")
		return
	}

	OKEmpty(w)
}

func (h *FeedbackHandler) GetByMessage(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	msgID, err := uuid.Parse(r.PathValue("messageId"))
	if err != nil {
		BadRequest(w, "invalid message id")
		return
	}

	fb, err := h.feedbackSvc.GetByMessageID(r.Context(), msgID, userID)
	if err != nil {
		NotFound(w, "feedback not found")
		return
	}

	OK(w, fb)
}
