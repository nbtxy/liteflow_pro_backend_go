package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
	stsProvider *storage.STSProvider
}

type imageThumbnailURLSigner interface {
	GenerateImageThumbnailURL(ctx context.Context, conversationID, path string, width, height, expireMinutes int) (string, error)
}

const maxUploadBaseNameLen = 80

var uploadNameSanitizeRegex = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func NewArtifactHandler(artifactSvc *artifact.Service, storageSvc storage.Service, stsProvider *storage.STSProvider) *ArtifactHandler {
	return &ArtifactHandler{artifactSvc: artifactSvc, storageSvc: storageSvc, stsProvider: stsProvider}
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
	h.CompleteUpload(w, r)
}

func (h *ArtifactHandler) GetUploadSTS(w http.ResponseWriter, r *http.Request) {
	_, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}
	if h.stsProvider == nil {
		InternalError(w, "STS provider is not configured")
		return
	}

	convID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid conversation id")
		return
	}
	var req struct {
		FileName string `json:"fileName"`
		Path     string `json:"path"`
	}
	if err := DecodeJSON(r, &req); err != nil {
		BadRequest(w, "invalid request body")
		return
	}
	baseName, err := normalizeUploadFileName(req.FileName)
	if err != nil {
		BadRequest(w, "invalid file name")
		return
	}
	path, err := normalizeOrGenerateUploadPath(req.Path, baseName)
	if err != nil {
		BadRequest(w, "invalid upload path")
		return
	}
	objectKey := fmt.Sprintf("conversations/%s/%s", convID.String(), path)
	sessionName := fmt.Sprintf("liteflow-%s", convID.String())
	cred, err := h.stsProvider.IssueUploadSTS(r.Context(), sessionName, objectKey)
	if err != nil {
		InternalError(w, "failed to generate upload sts")
		return
	}

	OK(w, map[string]any{
		"region":          cred.Region,
		"endpoint":        cred.Endpoint,
		"bucket":          cred.Bucket,
		"accessKeyId":     cred.AccessKeyID,
		"accessKeySecret": cred.AccessKeySecret,
		"securityToken":   cred.SecurityToken,
		"expiration":      cred.Expiration,
		"objectKey":       objectKey,
		"path":            path,
	})
}

func (h *ArtifactHandler) CompleteUpload(w http.ResponseWriter, r *http.Request) {
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

	var body struct {
		Path     string `json:"path"`
		FileName string `json:"fileName"`
		Size     int64  `json:"size"`
		MimeType string `json:"mimeType"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		BadRequest(w, "invalid request body")
		return
	}
	if body.Size <= 0 {
		BadRequest(w, "empty file is not allowed")
		return
	}
	storedPath := strings.TrimPrefix(strings.TrimSpace(body.Path), "/")
	if storedPath == "" || strings.Contains(storedPath, "..") || strings.HasPrefix(storedPath, "/") {
		BadRequest(w, "invalid file path")
		return
	}
	baseName, nameErr := normalizeUploadFileName(body.FileName)
	if nameErr != nil {
		baseName, nameErr = normalizeUploadFileName(filepath.Base(storedPath))
	}
	if nameErr != nil {
		BadRequest(w, "invalid file name")
		return
	}
	if !strings.HasPrefix(storedPath, "uploads/") {
		BadRequest(w, "invalid upload path")
		return
	}

	if _, readErr := h.storageSvc.ReadFile(r.Context(), convID.String(), storedPath); readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			BadRequest(w, "uploaded file not found")
		} else {
			InternalError(w, "failed to verify uploaded file")
		}
		return
	}

	typ := guessArtifactType(baseName, body.MimeType)
	size := body.Size
	title := baseName
	metaBytes, _ := json.Marshal(map[string]any{
		"source":    "upload",
		"mime_type": body.MimeType,
	})

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

func (h *ArtifactHandler) GetThumbnailURL(w http.ResponseWriter, r *http.Request) {
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

	width := 160
	if raw := strings.TrimSpace(r.URL.Query().Get("w")); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value <= 0 || value > 1024 {
			BadRequest(w, "invalid width")
			return
		}
		width = value
	}

	height := 160
	if raw := strings.TrimSpace(r.URL.Query().Get("h")); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value <= 0 || value > 1024 {
			BadRequest(w, "invalid height")
			return
		}
		height = value
	}

	signer, ok := h.storageSvc.(imageThumbnailURLSigner)
	if !ok {
		InternalError(w, "thumbnail signer is not configured")
		return
	}

	url, err := signer.GenerateImageThumbnailURL(r.Context(), convID.String(), filePath, width, height, 10)
	if err != nil {
		InternalError(w, "failed to generate thumbnail url")
		return
	}

	OK(w, map[string]any{"url": url})
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

func normalizeUploadFileName(raw string) (string, error) {
	name := filepath.Base(strings.TrimSpace(raw))
	if name == "" || name == "." || name == "/" {
		return "", fmt.Errorf("invalid file name")
	}

	name = uploadNameSanitizeRegex.ReplaceAllString(name, "_")
	name = strings.Trim(name, "._-")
	if name == "" {
		return "", fmt.Errorf("invalid file name")
	}

	ext := strings.ToLower(filepath.Ext(name))
	base := strings.TrimSuffix(name, ext)
	base = strings.Trim(base, "._-")
	if base == "" {
		base = "file"
	}
	if len(base) > maxUploadBaseNameLen {
		base = base[:maxUploadBaseNameLen]
		base = strings.TrimRight(base, "._-")
		if base == "" {
			base = "file"
		}
	}

	if ext != "" {
		if len(ext) > 12 || strings.Contains(ext, "..") {
			ext = ""
		}
	}
	return base + ext, nil
}

func normalizeOrGenerateUploadPath(requestPath, normalizedBaseName string) (string, error) {
	candidate := strings.TrimPrefix(strings.TrimSpace(requestPath), "/")
	if candidate == "" {
		return fmt.Sprintf("uploads/%d_%s", time.Now().UnixMilli(), normalizedBaseName), nil
	}
	if strings.Contains(candidate, "..") || strings.HasPrefix(candidate, "/") {
		return "", fmt.Errorf("invalid path")
	}
	if !strings.HasPrefix(candidate, "uploads/") {
		return "", fmt.Errorf("invalid path")
	}
	filePart := filepath.Base(candidate)
	if filePart == "" || filePart == "." || filePart == "/" {
		return "", fmt.Errorf("invalid path")
	}
	return candidate, nil
}
