package api

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/auth"
	"github.com/liteflow/backend/internal/conversation"
	"github.com/liteflow/backend/internal/domain"
)

type ConversationHandler struct {
	convSvc *conversation.Service
}

func NewConversationHandler(convSvc *conversation.Service) *ConversationHandler {
	return &ConversationHandler{convSvc: convSvc}
}

func (h *ConversationHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	conv, err := h.convSvc.Create(r.Context(), userID)
	if err != nil {
		InternalError(w, "failed to create conversation")
		return
	}

	OK(w, conv)
}

func (h *ConversationHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	if r.URL.Query().Get("archived") == "true" {
		convs, err := h.convSvc.ListArchived(r.Context(), userID)
		if err != nil {
			InternalError(w, "failed to list archived conversations")
			return
		}
		if convs == nil {
			convs = []domain.Conversation{}
		}
		OK(w, convs)
		return
	}

	pageStr := r.URL.Query().Get("page")
	sizeStr := r.URL.Query().Get("size")

	if pageStr != "" {
		page, _ := strconv.Atoi(pageStr)
		size, _ := strconv.Atoi(sizeStr)
		if size <= 0 || size > 100 {
			size = 20
		}
		offset := page * size

		convs, err := h.convSvc.ListPaged(r.Context(), userID, size, offset)
		if err != nil {
			InternalError(w, "failed to list conversations")
			return
		}
		if convs == nil {
			convs = []domain.Conversation{}
		}
		total, _ := h.convSvc.CountActive(r.Context(), userID)
		totalPages := int((total + int64(size) - 1) / int64(size))

		OK(w, map[string]any{
			"content":       convs,
			"totalElements": total,
			"totalPages":    totalPages,
			"page":          page,
			"size":          size,
		})
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	convs, err := h.convSvc.ListPaged(r.Context(), userID, limit, offset)
	if err != nil {
		InternalError(w, "failed to list conversations")
		return
	}
	if convs == nil {
		convs = []domain.Conversation{}
	}

	OK(w, convs)
}

func (h *ConversationHandler) GetMessages(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid conversation id")
		return
	}

	conv, err := h.convSvc.GetByIDAndUserID(r.Context(), convID, userID)
	if err != nil || conv == nil {
		NotFound(w, "conversation not found")
		return
	}

	msgs, err := h.convSvc.GetMessages(r.Context(), convID)
	if err != nil {
		InternalError(w, "failed to get messages")
		return
	}

	OK(w, msgs)
}

func (h *ConversationHandler) UpdateTitle(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid conversation id")
		return
	}

	conv, err := h.convSvc.GetByIDAndUserID(r.Context(), convID, userID)
	if err != nil || conv == nil {
		NotFound(w, "conversation not found")
		return
	}

	var body struct {
		Title string `json:"title"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.Title == "" {
		BadRequest(w, "title is required")
		return
	}

	if err := h.convSvc.UpdateTitle(r.Context(), convID, body.Title); err != nil {
		InternalError(w, "failed to update title")
		return
	}

	OKEmpty(w)
}

func (h *ConversationHandler) Archive(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid conversation id")
		return
	}

	if err := h.convSvc.Archive(r.Context(), convID, userID); err != nil {
		InternalError(w, "failed to archive conversation")
		return
	}

	conv, _ := h.convSvc.GetByIDAndUserID(r.Context(), convID, userID)
	if conv != nil {
		OK(w, conv)
	} else {
		OKEmpty(w)
	}
}

func (h *ConversationHandler) Unarchive(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid conversation id")
		return
	}

	if err := h.convSvc.Unarchive(r.Context(), convID, userID); err != nil {
		InternalError(w, "failed to unarchive conversation")
		return
	}

	conv, _ := h.convSvc.GetByIDAndUserID(r.Context(), convID, userID)
	if conv != nil {
		OK(w, conv)
	} else {
		OKEmpty(w)
	}
}

func (h *ConversationHandler) Batch(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	var body struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		BadRequest(w, "invalid request body")
		return
	}
	if body.Action == "" || len(body.IDs) == 0 {
		BadRequest(w, "action and ids are required")
		return
	}

	ids := make([]uuid.UUID, 0, len(body.IDs))
	for _, s := range body.IDs {
		id, err := uuid.Parse(s)
		if err != nil {
			BadRequest(w, "invalid id: "+s)
			return
		}
		ids = append(ids, id)
	}

	switch body.Action {
	case "delete":
		err = h.convSvc.BatchDelete(r.Context(), userID, ids)
	case "archive":
		err = h.convSvc.BatchArchive(r.Context(), userID, ids)
	default:
		BadRequest(w, "unknown action: "+body.Action)
		return
	}

	if err != nil {
		InternalError(w, "batch operation failed")
		return
	}
	OKEmpty(w)
}

func (h *ConversationHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid conversation id")
		return
	}

	if err := h.convSvc.Delete(r.Context(), convID, userID); err != nil {
		InternalError(w, "failed to delete conversation")
		return
	}

	OKEmpty(w)
}

func (h *ConversationHandler) ClearMessages(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid conversation id")
		return
	}

	if err := h.convSvc.ClearMessages(r.Context(), convID, userID); err != nil {
		InternalError(w, "failed to clear messages")
		return
	}

	OKEmpty(w)
}
