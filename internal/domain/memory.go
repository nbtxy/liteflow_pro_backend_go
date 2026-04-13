package domain

import (
	"time"

	"github.com/google/uuid"
)

type MemoryCategory string

const (
	MemoryCategoryIdentity    MemoryCategory = "IDENTITY"
	MemoryCategoryPreference  MemoryCategory = "PREFERENCE"
	MemoryCategoryProject     MemoryCategory = "PROJECT"
	MemoryCategorySkill       MemoryCategory = "SKILL"
	MemoryCategoryContext     MemoryCategory = "CONTEXT"
	MemoryCategoryPersonal    MemoryCategory = "PERSONAL"
	MemoryCategoryInstruction MemoryCategory = "INSTRUCTION"
)

type UserMemory struct {
	ID                   uuid.UUID  `json:"id"`
	UserID               uuid.UUID  `json:"userId"`
	Category             string     `json:"category"`
	Content              string     `json:"content"`
	SourceConversationID *uuid.UUID `json:"sourceConversationId,omitempty"`
	Confidence           *float32   `json:"confidence,omitempty"`
	IsActive             *bool      `json:"isActive,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}
