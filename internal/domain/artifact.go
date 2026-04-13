package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ArtifactType string

const (
	ArtifactTypeCode     ArtifactType = "CODE"
	ArtifactTypeHTML     ArtifactType = "HTML"
	ArtifactTypeReact    ArtifactType = "REACT"
	ArtifactTypeMarkdown ArtifactType = "MARKDOWN"
	ArtifactTypeSVG      ArtifactType = "SVG"
	ArtifactTypeImage    ArtifactType = "IMAGE"
	ArtifactTypeData     ArtifactType = "DATA"
	ArtifactTypeFile     ArtifactType = "FILE"
)

type Artifact struct {
	ID             uuid.UUID       `json:"id"`
	ConversationID uuid.UUID       `json:"conversationId"`
	MessageID      *uuid.UUID      `json:"messageId,omitempty"`
	FilePath       string          `json:"filePath"`
	Type           string          `json:"type"`
	Title          *string         `json:"title,omitempty"`
	FileSize       *int64          `json:"fileSize,omitempty"`
	Version        int32           `json:"version"`
	ParentID       *uuid.UUID      `json:"parentId,omitempty"`
	StorageType    *string         `json:"storageType,omitempty"`
	UploadStatus   *string         `json:"uploadStatus,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	FileDeleted    *bool           `json:"fileDeleted,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
}
