package api

import (
	"fmt"
	"net/http"
	"time"

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

func (h *UsageHandler) GetUserUsage(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	from, to, err := parseUsagePeriod(r)
	if err != nil {
		BadRequest(w, err.Error())
		return
	}

	data, err := h.usageSvc.GetUserUsage(r.Context(), userID, from, to)
	if err != nil {
		InternalError(w, "failed to get user usage")
		return
	}

	OK(w, data)
}

func parseUsagePeriod(r *http.Request) (time.Time, time.Time, error) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "month"
	}

	now := time.Now()
	to := now
	switch period {
	case "today":
		from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return from, to, nil
	case "week":
		from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -7)
		return from, to, nil
	case "month":
		from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -30)
		return from, to, nil
	case "custom":
		fromStr := r.URL.Query().Get("from")
		toStr := r.URL.Query().Get("to")
		if fromStr == "" || toStr == "" {
			return time.Time{}, time.Time{}, fmt.Errorf("custom period requires from and to (RFC3339)")
		}
		from, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid from, must be RFC3339")
		}
		to, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid to, must be RFC3339")
		}
		return from, to, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("invalid period, must be today/week/month/custom")
	}
}
