package api

import (
	"net/http"

	"github.com/liteflow/backend/internal/auth"
	"github.com/liteflow/backend/internal/platform/storage"
)

type OssHandler struct {
	stsSvc *storage.OssStsService
}

func NewOssHandler(stsSvc *storage.OssStsService) *OssHandler {
	return &OssHandler{stsSvc: stsSvc}
}

func (h *OssHandler) GetSTSCredentials(w http.ResponseWriter, r *http.Request) {
	_, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	conversationID := r.URL.Query().Get("conversationId")
	if conversationID == "" {
		BadRequest(w, "conversationId is required")
		return
	}

	if h.stsSvc == nil {
		Error(w, http.StatusServiceUnavailable, 50300, "OSS not configured")
		return
	}

	creds, err := h.stsSvc.GetSTSCredentials(conversationID)
	if err != nil {
		InternalError(w, "failed to get STS credentials")
		return
	}

	OK(w, creds)
}
