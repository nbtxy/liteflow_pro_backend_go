package api

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/auth"
	"github.com/liteflow/backend/internal/memory"
)

type MemoryHandler struct {
	memorySvc *memory.Service
}

func NewMemoryHandler(memorySvc *memory.Service) *MemoryHandler {
	return &MemoryHandler{memorySvc: memorySvc}
}

func (h *MemoryHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	memories, err := h.memorySvc.GetByUserID(r.Context(), userID)
	if err != nil {
		InternalError(w, "failed to get memories")
		return
	}

	OK(w, memories)
}

func (h *MemoryHandler) Add(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	var body struct {
		Category string `json:"category"`
		Content  string `json:"content"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.Category == "" || body.Content == "" {
		BadRequest(w, "category and content are required")
		return
	}

	if err := h.memorySvc.Add(r.Context(), userID, body.Category, body.Content, nil); err != nil {
		InternalError(w, "failed to add memory")
		return
	}

	OKMsg(w, "memory added")
}

func (h *MemoryHandler) Update(w http.ResponseWriter, r *http.Request) {
	_, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	memID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid memory id")
		return
	}

	var body struct {
		Content string `json:"content"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.Content == "" {
		BadRequest(w, "content is required")
		return
	}

	if err := h.memorySvc.Update(r.Context(), memID, body.Content); err != nil {
		InternalError(w, "failed to update memory")
		return
	}

	OKEmpty(w)
}

func (h *MemoryHandler) Delete(w http.ResponseWriter, r *http.Request) {
	_, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	memID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid memory id")
		return
	}

	if err := h.memorySvc.HardDelete(r.Context(), memID); err != nil {
		InternalError(w, "failed to delete memory")
		return
	}

	OKEmpty(w)
}
