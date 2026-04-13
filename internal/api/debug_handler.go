package api

import (
	"net/http"
	"runtime"

	"github.com/liteflow/backend/internal/auth"
)

type DebugHandler struct {
	authSvc *auth.AuthService
	enabled bool
}

func NewDebugHandler(authSvc *auth.AuthService, enabled bool) *DebugHandler {
	return &DebugHandler{authSvc: authSvc, enabled: enabled}
}

func (h *DebugHandler) GetLatestCode(w http.ResponseWriter, r *http.Request) {
	if !h.enabled {
		NotFound(w, "debug endpoints disabled")
		return
	}

	phone := r.URL.Query().Get("phone")
	if phone == "" {
		BadRequest(w, "phone is required")
		return
	}

	code, err := h.authSvc.GetLatestCode(r.Context(), phone)
	if err != nil {
		NotFound(w, "no active code found")
		return
	}

	OK(w, map[string]string{"code": code})
}

func (h *DebugHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	OK(w, map[string]any{
		"status":      "ok",
		"goroutines":  runtime.NumGoroutine(),
		"memoryAlloc": m.Alloc,
		"goVersion":   runtime.Version(),
	})
}
