package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/artifact"
	"github.com/liteflow/backend/internal/auth"
	"github.com/liteflow/backend/internal/domain"
	"github.com/liteflow/backend/internal/platform/storage"
)

type ArtifactHandler struct {
	artifactSvc *artifact.Service
	storageSvc  storage.Service
}

func NewArtifactHandler(artifactSvc *artifact.Service, storageSvc storage.Service) *ArtifactHandler {
	return &ArtifactHandler{artifactSvc: artifactSvc, storageSvc: storageSvc}
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
		ID      string `json:"id"`
		Name    string `json:"name"`
		Path    string `json:"path"`
		Size    int64  `json:"size"`
		Version int32  `json:"version"`
		Type    string `json:"type"`
		Source  string `json:"source"`
		Created string `json:"createdAt"`
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
		source := "generated"
		if len(a.Metadata) > 0 {
			var meta struct {
				Source string `json:"source"`
			}
			if err := json.Unmarshal(a.Metadata, &meta); err == nil && meta.Source != "" {
				source = strings.ToLower(meta.Source)
			}
		}
		if source != "upload" {
			source = "generated"
		}
		files = append(files, fileInfo{
			ID:      a.ID.String(),
			Name:    name,
			Path:    a.FilePath,
			Size:    size,
			Version: a.Version,
			Type:    a.Type,
			Source:  source,
			Created: a.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	OK(w, files)
}

func (h *ArtifactHandler) UploadFile(w http.ResponseWriter, r *http.Request) {
	_, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid conversation id")
		return
	}

	if parseErr := r.ParseMultipartForm(12 << 20); parseErr != nil {
		BadRequest(w, "invalid multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		BadRequest(w, "file is required")
		return
	}
	defer file.Close()

	if header.Size <= 0 {
		BadRequest(w, "empty file is not allowed")
		return
	}

	content, readErr := io.ReadAll(file)
	if readErr != nil {
		InternalError(w, "failed to read uploaded file")
		return
	}
	if len(content) == 0 {
		BadRequest(w, "empty file is not allowed")
		return
	}

	baseName := filepath.Base(header.Filename)
	if baseName == "." || baseName == "/" || baseName == "" {
		BadRequest(w, "invalid file name")
		return
	}

	safeName := strings.ReplaceAll(baseName, " ", "_")
	storedPath := fmt.Sprintf("uploads/%d_%s", time.Now().UnixMilli(), safeName)
	typ := guessArtifactType(baseName, header.Header.Get("Content-Type"))
	size := int64(len(content))
	title := baseName
	metaBytes, _ := json.Marshal(map[string]any{
		"source":    "upload",
		"mime_type": header.Header.Get("Content-Type"),
	})

	if err := h.storageSvc.UploadFile(r.Context(), convID.String(), storedPath, content); err != nil {
		InternalError(w, "failed to store uploaded file")
		return
	}

	a := &domain.Artifact{
		ConversationID: convID,
		FilePath:       storedPath,
		Type:           typ,
		Title:          &title,
		FileSize:       &size,
		Version:        1,
		Metadata:       metaBytes,
		CreatedAt:      time.Now(),
	}
	if err := h.artifactSvc.Create(r.Context(), a); err != nil {
		InternalError(w, "failed to save uploaded file")
		return
	}

	OK(w, map[string]any{
		"id":         a.ID.String(),
		"path":       a.FilePath,
		"name":       title,
		"size":       size,
		"type":       a.Type,
		"source":     "upload",
		"artifactId": a.ID.String(),
		"version":    a.Version,
	})
}

func (h *ArtifactHandler) DownloadFile(w http.ResponseWriter, r *http.Request) {
	_, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid conversation id")
		return
	}

	filePath := strings.TrimSpace(r.URL.Query().Get("path"))
	if filePath == "" {
		BadRequest(w, "path is required")
		return
	}

	content, err := h.storageSvc.ReadFile(r.Context(), convID.String(), filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			NotFound(w, "file not found")
			return
		}
		InternalError(w, "failed to read file")
		return
	}

	filename := filepath.Base(filePath)
	if filename == "" || filename == "." || filename == "/" {
		filename = "download.bin"
	}
	w.Header().Set("Content-Type", http.DetectContentType(content))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (h *ArtifactHandler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	_, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid conversation id")
		return
	}

	filePath := strings.TrimSpace(r.URL.Query().Get("path"))
	if filePath == "" {
		BadRequest(w, "path is required")
		return
	}

	if err := h.storageSvc.DeleteFile(r.Context(), convID.String(), filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		InternalError(w, "failed to delete file")
		return
	}
	if err := h.artifactSvc.DeleteByPath(r.Context(), convID, filePath); err != nil {
		InternalError(w, "failed to mark artifact deleted")
		return
	}

	OKEmpty(w)
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

func guessArtifactType(name, mimeType string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".py", ".js", ".ts", ".tsx", ".jsx", ".java", ".c", ".cpp", ".go", ".rs", ".json", ".yaml", ".yml", ".xml", ".sql":
		return string(domain.ArtifactTypeCode)
	case ".html", ".htm":
		return string(domain.ArtifactTypeHTML)
	case ".md", ".markdown":
		return string(domain.ArtifactTypeMarkdown)
	case ".svg":
		return string(domain.ArtifactTypeSVG)
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return string(domain.ArtifactTypeImage)
	case ".csv", ".tsv":
		return string(domain.ArtifactTypeData)
	}
	if strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		return string(domain.ArtifactTypeImage)
	}
	return string(domain.ArtifactTypeFile)
}
