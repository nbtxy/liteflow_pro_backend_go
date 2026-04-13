package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/auth"
	"github.com/liteflow/backend/internal/domain"
	"github.com/liteflow/backend/internal/task"
)

type TaskHandler struct {
	scheduler *task.Scheduler
	executor  *task.Executor
}

func NewTaskHandler(scheduler *task.Scheduler, executor *task.Executor) *TaskHandler {
	return &TaskHandler{scheduler: scheduler, executor: executor}
}

func (h *TaskHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	tasks, err := h.scheduler.GetUserTasks(r.Context(), userID)
	if err != nil {
		InternalError(w, "failed to list tasks")
		return
	}

	OK(w, tasks)
}

func (h *TaskHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	var body struct {
		Name            string          `json:"name"`
		Prompt          string          `json:"prompt"`
		TaskType        string          `json:"taskType"`
		CronExpression  *string         `json:"cronExpression,omitempty"`
		RunAt           *string         `json:"runAt,omitempty"`
		Timezone        string          `json:"timezone"`
		ConversationID  *string         `json:"conversationId,omitempty"`
		SourceMessageID *string         `json:"sourceMessageId,omitempty"`
		OutputConfig    json.RawMessage `json:"outputConfig,omitempty"`
		MaxTokens       int32           `json:"maxTokens"`
		EnableTools     bool            `json:"enableTools"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		BadRequest(w, "invalid request body")
		return
	}

	var runAt *time.Time
	if body.RunAt != nil && *body.RunAt != "" {
		parsed, err := time.Parse(time.RFC3339, *body.RunAt)
		if err != nil {
			BadRequest(w, "invalid runAt, must be RFC3339")
			return
		}
		runAt = &parsed
	}

	var conversationID *uuid.UUID
	if body.ConversationID != nil && *body.ConversationID != "" {
		parsed, err := uuid.Parse(*body.ConversationID)
		if err != nil {
			BadRequest(w, "invalid conversationId")
			return
		}
		conversationID = &parsed
	}

	var sourceMessageID *uuid.UUID
	if body.SourceMessageID != nil && *body.SourceMessageID != "" {
		parsed, err := uuid.Parse(*body.SourceMessageID)
		if err != nil {
			BadRequest(w, "invalid sourceMessageId")
			return
		}
		sourceMessageID = &parsed
	}

	t := &domain.ScheduledTask{
		UserID:          userID,
		ConversationID:  conversationID,
		SourceMessageID: sourceMessageID,
		Name:            body.Name,
		Prompt:          body.Prompt,
		TaskType:        body.TaskType,
		CronExpression:  body.CronExpression,
		RunAt:           runAt,
		Timezone:        body.Timezone,
		OutputConfig:    body.OutputConfig,
		MaxTokens:       body.MaxTokens,
		EnableTools:     body.EnableTools,
		Status:          "active",
	}

	if err := h.scheduler.CreateTaskAndSchedule(r.Context(), t); err != nil {
		InternalError(w, "failed to create task")
		return
	}

	OK(w, t)
}

func (h *TaskHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	taskID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid task id")
		return
	}

	if err := h.scheduler.DeleteTaskAndCancel(r.Context(), taskID, userID); err != nil {
		InternalError(w, "failed to delete task")
		return
	}

	OKEmpty(w)
}

func (h *TaskHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	_, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	taskID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid task id")
		return
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.Status == "" {
		BadRequest(w, "status is required")
		return
	}

	if err := h.scheduler.UpdateTaskStatus(r.Context(), taskID, body.Status); err != nil {
		InternalError(w, "failed to update task status")
		return
	}

	OKEmpty(w)
}

func (h *TaskHandler) Pause(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	taskID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid task id")
		return
	}

	if err := h.scheduler.PauseTask(r.Context(), taskID, userID); err != nil {
		InternalError(w, "failed to pause task")
		return
	}

	OKEmpty(w)
}

func (h *TaskHandler) Resume(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	taskID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid task id")
		return
	}

	if err := h.scheduler.ResumeTask(r.Context(), taskID, userID); err != nil {
		InternalError(w, "failed to resume task")
		return
	}

	OKEmpty(w)
}

func (h *TaskHandler) RunNow(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	taskID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid task id")
		return
	}

	task, err := h.scheduler.GetTaskByIDAndUser(r.Context(), taskID, userID)
	if err != nil {
		NotFound(w, "task not found")
		return
	}

	go h.executor.Run(r.Context(), task)

	OK(w, map[string]string{"message": "task execution started"})
}

func (h *TaskHandler) GetExecutions(w http.ResponseWriter, r *http.Request) {
	_, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	taskID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid task id")
		return
	}

	page := 0
	size := 5
	if p := r.URL.Query().Get("page"); p != "" {
		fmt.Sscanf(p, "%d", &page)
	}
	if s := r.URL.Query().Get("size"); s != "" {
		fmt.Sscanf(s, "%d", &size)
	}
	if size <= 0 || size > 50 {
		size = 5
	}

	total, err := h.executor.CountExecutions(r.Context(), taskID)
	if err != nil {
		InternalError(w, "failed to count executions")
		return
	}

	execs, err := h.executor.GetExecutionsPaged(r.Context(), taskID, page, size)
	if err != nil {
		InternalError(w, "failed to get executions")
		return
	}

	totalPages := (total + size - 1) / size
	if totalPages < 1 {
		totalPages = 1
	}

	OK(w, map[string]any{
		"content":    execs,
		"totalPages": totalPages,
		"page":       page,
		"size":       size,
	})
}

func (h *TaskHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	taskID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid task id")
		return
	}

	task, err := h.scheduler.GetTaskByIDAndUser(r.Context(), taskID, userID)
	if err != nil {
		NotFound(w, "task not found")
		return
	}
	OK(w, task)
}

func (h *TaskHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	taskID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		BadRequest(w, "invalid task id")
		return
	}

	var body struct {
		Name           *string          `json:"name"`
		Prompt         *string          `json:"prompt"`
		CronExpression *string          `json:"cronExpression"`
		Timezone       *string          `json:"timezone"`
		OutputConfig   *json.RawMessage `json:"outputConfig"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		BadRequest(w, "invalid request body")
		return
	}

	task, err := h.scheduler.UpdateTaskByUser(r.Context(), taskID, userID, task.TaskUpdateInput{
		Name:           body.Name,
		Prompt:         body.Prompt,
		CronExpression: body.CronExpression,
		Timezone:       body.Timezone,
		OutputConfig:   body.OutputConfig,
	})
	if err != nil {
		InternalError(w, "failed to update task")
		return
	}

	OK(w, task)
}
