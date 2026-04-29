package api

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/admin"
)

type AdminHandler struct {
	adminSvc *admin.Service
}

func NewAdminHandler(adminSvc *admin.Service) *AdminHandler {
	return &AdminHandler{adminSvc: adminSvc}
}

func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.adminSvc.ListUsers(r.Context(), 100, 0)
	if err != nil {
		InternalError(w, "failed to list users")
		return
	}
	OK(w, users)
}

func (h *AdminHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.adminSvc.GetSystemStats(r.Context())
	if err != nil {
		InternalError(w, "failed to get stats")
		return
	}
	OK(w, stats)
}

func (h *AdminHandler) ListFeedbacks(w http.ResponseWriter, r *http.Request) {
	limit := 100
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	feedbacks, err := h.adminSvc.ListFeedbacks(r.Context(), limit, offset)
	if err != nil {
		InternalError(w, "failed to list feedbacks")
		return
	}
	OK(w, feedbacks)
}

func (h *AdminHandler) SetAdmin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID  string `json:"userId"`
		IsAdmin bool   `json:"isAdmin"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		BadRequest(w, "invalid request")
		return
	}

	userID, err := uuid.Parse(body.UserID)
	if err != nil {
		BadRequest(w, "invalid userId")
		return
	}

	if err := h.adminSvc.SetAdmin(r.Context(), userID, body.IsAdmin); err != nil {
		InternalError(w, "failed to set admin")
		return
	}

	OKEmpty(w)
}

func (h *AdminHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid user id")
		return
	}

	if err := h.adminSvc.DeleteUser(r.Context(), userID); err != nil {
		InternalError(w, "failed to delete user")
		return
	}

	OKEmpty(w)
}
