package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ScheduledTask struct {
	ID              uuid.UUID       `json:"id"`
	UserID          uuid.UUID       `json:"userId"`
	ConversationID  *uuid.UUID      `json:"conversationId,omitempty"`
	SourceMessageID *uuid.UUID      `json:"sourceMessageId,omitempty"`
	Name            string          `json:"name"`
	Prompt          string          `json:"prompt"`
	TaskType        string          `json:"taskType"`        // recurring, once
	CronExpression  *string         `json:"cronExpression,omitempty"`
	RunAt           *time.Time      `json:"runAt,omitempty"`
	Timezone        string          `json:"timezone"`
	OutputConfig    json.RawMessage `json:"outputConfig"`
	Status          string          `json:"status"`          // active, paused, completed, failed
	MaxTokens       int32           `json:"maxTokens"`
	EnableTools     bool            `json:"enableTools"`
	LastRunAt       *time.Time      `json:"lastRunAt,omitempty"`
	LastRunStatus   *string         `json:"lastRunStatus,omitempty"`
	TotalRuns       int32           `json:"totalRuns"`
	TotalTokens     int64           `json:"totalTokens"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

type TaskExecution struct {
	ID            uuid.UUID       `json:"id"`
	TaskID        uuid.UUID       `json:"taskId"`
	Status        string          `json:"status"`
	ResultSummary *string         `json:"resultSummary,omitempty"`
	OutputTargets json.RawMessage `json:"outputTargets,omitempty"`
	InputTokens   *int32          `json:"inputTokens,omitempty"`
	OutputTokens  *int32          `json:"outputTokens,omitempty"`
	ToolsUsed     json.RawMessage `json:"toolsUsed,omitempty"`
	DurationMs    *int32          `json:"durationMs,omitempty"`
	ErrorMessage  *string         `json:"errorMessage,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
}
