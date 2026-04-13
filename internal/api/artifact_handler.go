package api

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/artifact"
	"github.com/liteflow/backend/internal/auth"
)

type ArtifactHandler struct {
	artifactSvc *artifact.Service
}

func NewArtifactHandler(artifactSvc *artifact.Service) *ArtifactHandler {
	return &ArtifactHandler{artifactSvc: artifactSvc}
}

func (h *ArtifactHandler) ListFiles(w http.ResponseWriter, r *http.Request) {
	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid conversation id")
		return
	}

	artifacts, err := h.artifactSvc.GetLatestArtifacts(r.Context(), convID)
	if err != nil {
		InternalError(w, "failed to list files")
		return
	}

	type fileInfo struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Path     string `json:"path"`
		Size     int64  `json:"size"`
		Version  int32  `json:"version"`
		Type     string `json:"type"`
		Created  string `json:"createdAt"`
	}

	files := make([]fileInfo, 0, len(artifacts))
	for _, a := range artifacts {
		name := a.FilePath
		if a.Title != nil && *a.Title != "" {
			name = *a.Title
		}
		var size int64
		if a.FileSize != nil {
			size = *a.FileSize
		}
		files = append(files, fileInfo{
			ID:      a.ID.String(),
			Name:    name,
			Path:    a.FilePath,
			Size:    size,
			Version: a.Version,
			Type:    a.Type,
			Created: a.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	OK(w, files)
}

func (h *ArtifactHandler) GetByConversation(w http.ResponseWriter, r *http.Request) {
	convID, err := uuid.Parse(r.PathValue("conversationId"))
	if err != nil {
		BadRequest(w, "invalid conversation id")
		return
	}

	artifacts, err := h.artifactSvc.GetByConversation(r.Context(), convID)
	if err != nil {
		InternalError(w, "failed to get artifacts")
		return
	}

	OK(w, artifacts)
}

func (h *ArtifactHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid artifact id")
		return
	}

	a, err := h.artifactSvc.GetByID(r.Context(), id)
	if err != nil {
		NotFound(w, "artifact not found")
		return
	}

	OK(w, a)
}

func (h *ArtifactHandler) GetVersionHistory(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid artifact id")
		return
	}

	versions, err := h.artifactSvc.GetVersionHistory(r.Context(), id)
	if err != nil {
		InternalError(w, "failed to get version history")
		return
	}

	OK(w, versions)
}

func (h *ArtifactHandler) Delete(w http.ResponseWriter, r *http.Request) {
	_, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid artifact id")
		return
	}

	if err := h.artifactSvc.Delete(r.Context(), id); err != nil {
		InternalError(w, "failed to delete artifact")
		return
	}

	OKEmpty(w)
}
