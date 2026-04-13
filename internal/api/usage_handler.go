package api

import (
	"net/http"

	"github.com/liteflow/backend/internal/auth"
	"github.com/liteflow/backend/internal/usage"
)

type UsageHandler struct {
	usageSvc *usage.Service
}

func NewUsageHandler(usageSvc *usage.Service) *UsageHandler {
	return &UsageHandler{usageSvc: usageSvc}
}

func (h *UsageHandler) GetDailySummary(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	summary, err := h.usageSvc.GetUserDailySummary(r.Context(), userID)
	if err != nil {
		InternalError(w, "failed to get usage summary")
		return
	}

	OK(w, summary)
}

func (h *UsageHandler) GetHistory(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	history, err := h.usageSvc.GetUserHistory(r.Context(), userID, 50)
	if err != nil {
		InternalError(w, "failed to get usage history")
		return
	}

	OK(w, history)
}
