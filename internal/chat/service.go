package chat

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/agent"
	"github.com/liteflow/backend/internal/conversation"
	"github.com/liteflow/backend/internal/domain"
	"github.com/liteflow/backend/internal/llm"
	"github.com/liteflow/backend/internal/task"
	"github.com/liteflow/backend/internal/tool"
	"github.com/liteflow/backend/internal/usage"
)

type Service struct {
	providerRouter *llm.ProviderRouter
	contextAsm     *llm.ContextAssembler
	toolRegistry   *tool.Registry
	agentLoop      *agent.AgentLoop
	convSvc        *conversation.Service
	usageSvc       *usage.Service
	taskScheduler  *task.Scheduler
}

func NewService(
	providerRouter *llm.ProviderRouter,
	contextAsm *llm.ContextAssembler,
	toolRegistry *tool.Registry,
	agentLoop *agent.AgentLoop,
	convSvc *conversation.Service,
	usageSvc *usage.Service,
) *Service {
	return &Service{
		providerRouter: providerRouter,
		contextAsm:     contextAsm,
		toolRegistry:   toolRegistry,
		agentLoop:      agentLoop,
		convSvc:        convSvc,
		usageSvc:       usageSvc,
	}
}

func (s *Service) SetTaskScheduler(ts *task.Scheduler) {
	s.taskScheduler = ts
}

type ChatRequest struct {
	ConversationID string            `json:"conversationId"`
	Message        string            `json:"message"`
	Attachments    []json.RawMessage `json:"attachments,omitempty"`
	QuotedMessage  json.RawMessage   `json:"quotedMessage,omitempty"`
}

func (s *Service) ChatStream(ctx context.Context, req ChatRequest, userID uuid.UUID) <-chan agent.Event {
	events := make(chan agent.Event, 64)

	go func() {
		defer close(events)
		s.doChatStream(ctx, req, userID, events)
	}()

	return events
}

func (s *Service) doChatStream(ctx context.Context, req ChatRequest, userID uuid.UUID, events chan<- agent.Event) {
	var conv *domain.Conversation
	var err error

	if req.ConversationID != "" {
		convID, parseErr := uuid.Parse(req.ConversationID)
		if parseErr != nil {
			events <- agent.ErrorEvent("invalid_conversation_id", "无效的会话ID")
			return
		}
		conv, err = s.convSvc.GetByIDAndUserID(ctx, convID, userID)
		if err != nil || conv == nil {
			events <- agent.ErrorEvent("forbidden", "会话不存在或无权访问")
			return
		}
	} else {
		conv, err = s.convSvc.Create(ctx, userID)
		if err != nil {
			events <- agent.ErrorEvent("create_conversation_failed", "创建会话失败")
			return
		}
	}

	userMsg := &domain.Message{
		ConversationID: conv.ID,
		Role:           "user",
		Content:        req.Message,
		CreatedAt:      time.Now(),
	}
	tokenCount := int32(len(req.Message))
	userMsg.TokenCount = &tokenCount

	if len(req.Attachments) > 0 || len(req.QuotedMessage) > 0 {
		meta := map[string]any{}
		if len(req.Attachments) > 0 {
			meta["attachments"] = req.Attachments
		}
		if len(req.QuotedMessage) > 0 {
			meta["quotedMessage"] = req.QuotedMessage
		}
		if metaBytes, err := json.Marshal(meta); err == nil {
			userMsg.Metadata = metaBytes
		}
	}

	if err := s.convSvc.SaveMessage(ctx, userMsg); err != nil {
		slog.Error("failed to save user message", "err", err)
		events <- agent.ErrorEvent("save_failed", "保存消息失败")
		return
	}

	history, err := s.convSvc.GetMessages(ctx, conv.ID)
	if err != nil {
		events <- agent.ErrorEvent("load_history_failed", "加载历史失败")
		return
	}

	mcpState := &conversation.MCPState{Mode: "inactive", ActivatedTools: []string{}}
	if state, stateErr := s.convSvc.GetMCPState(ctx, conv.ID, userID); stateErr != nil {
		slog.Warn("failed to load conversation MCP state", "conversationId", conv.ID, "err", stateErr)
	} else {
		mcpState = state
	}

	llmReq := s.contextAsm.Assemble(history, conv.ID.String(), "", "")

	assistantMsgID := uuid.New()
	assistantMsg := &domain.Message{
		ID:             assistantMsgID,
		ConversationID: conv.ID,
		Role:           "assistant",
		Content:        "",
		CreatedAt:      time.Now(),
	}
	if err := s.convSvc.SaveMessage(ctx, assistantMsg); err != nil {
		slog.Error("failed to create assistant placeholder message", "err", err)
		events <- agent.ErrorEvent("save_failed", "保存消息失败")
		return
	}
	usageAcc := &llm.LlmUsage{}
	startTime := time.Now()

	events <- agent.NewEvent(agent.EventStreamStart, map[string]any{
		"messageId":      assistantMsgID.String(),
		"conversationId": conv.ID.String(),
	})

	var fullContent strings.Builder
	var contentParts []map[string]any
	toolUseIndex := make(map[string]map[string]any)

	hasTools := len(s.toolRegistry.All()) > 0
	var purpose string
	if hasTools {
		purpose = "chat_with_tools"
		currentMcpState := &conversation.MCPState{
			Mode:            mcpState.Mode,
			ActivatedTools:  append([]string(nil), mcpState.ActivatedTools...),
			SourceMessageID: mcpState.SourceMessageID,
		}
		toolCtx := &tool.ToolContext{
			ConversationID: conv.ID,
			MessageID:      assistantMsgID,
			UserID:         userID,
		}

		var textAccum strings.Builder // accumulate text between tool calls

		agentEvents := s.agentLoop.Execute(ctx, llmReq, toolCtx, usageAcc, &agent.ExecuteOptions{
			MCPMode:        currentMcpState.Mode,
			ActivatedTools: currentMcpState.ActivatedTools,
		})
		for ev := range agentEvents {
			switch ev.Type {
			case agent.EventTextDelta:
				if content, ok := ev.Data["content"].(string); ok {
					fullContent.WriteString(content)
					textAccum.WriteString(content)
				}
			case agent.EventToolUseStart:
				// Flush accumulated text as a text part
				if textAccum.Len() > 0 {
					contentParts = append(contentParts, map[string]any{"type": "text", "text": textAccum.String()})
					textAccum.Reset()
				}
				startedAt := time.Now().UnixMilli()
				tc := map[string]any{
					"toolUseId": ev.Data["toolUseId"],
					"toolName":  ev.Data["toolName"],
					"status":    "running",
					"startedAt": startedAt,
				}
				if toolUseID, _ := ev.Data["toolUseId"].(string); toolUseID != "" {
					toolUseIndex[toolUseID] = tc
				}
				contentParts = append(contentParts, map[string]any{"type": "tool_use", "toolCall": tc})
			case agent.EventToolUseInput:
				toolUseID, _ := ev.Data["toolUseId"].(string)
				if tc, ok := toolUseIndex[toolUseID]; ok {
					tc["input"] = ev.Data["input"]
				}
			case agent.EventToolResult:
				toolUseID, _ := ev.Data["toolUseId"].(string)
				status, _ := ev.Data["status"].(string)
				content, _ := ev.Data["content"].(string)
				if mode, ok := ev.Data["mcp_mode"].(string); ok {
					currentMcpState.Mode = mode
					currentMcpState.ActivatedTools = parseStringSlice(ev.Data["activated_tools"])
					if sourceMessageID := parseStringPointer(ev.Data["source_message_id"]); sourceMessageID != nil {
						currentMcpState.SourceMessageID = sourceMessageID
					} else if mode != "active" {
						currentMcpState.SourceMessageID = nil
					}
				}
				if tc, ok := toolUseIndex[toolUseID]; ok {
					tc["status"] = status
					if startedAt, ok := tc["startedAt"].(int64); ok && startedAt > 0 {
						tc["duration"] = time.Now().UnixMilli() - startedAt
					}
				}
				contentParts = append(contentParts, map[string]any{
					"type":      "tool_result",
					"toolUseId": toolUseID,
					"status":    status,
					"content":   content,
				})
			}
			events <- ev
		}

		// Flush remaining text
		if textAccum.Len() > 0 {
			contentParts = append(contentParts, map[string]any{"type": "text", "text": textAccum.String()})
		}
		if err := s.convSvc.SetMCPState(ctx, conv.ID, userID, currentMcpState); err != nil {
			slog.Warn("failed to persist conversation MCP state", "conversationId", conv.ID, "err", err)
		}
	} else {
		purpose = "chat"
		provider := s.providerRouter.Default()
		if provider == nil {
			events <- agent.ErrorEvent("no_provider", "没有可用的LLM提供者")
			return
		}

		chunks, err := provider.StreamChat(ctx, llmReq)
		if err != nil {
			events <- agent.ErrorEvent("stream_error", "流式调用失败: "+err.Error())
			return
		}

		for chunk := range chunks {
			if chunk.Content != "" {
				fullContent.WriteString(chunk.Content)
				events <- agent.TextDeltaEvent(chunk.Content)
			}
			if chunk.Usage != nil {
				usageAcc.Add(chunk.Usage)
			}
		}
	}

	var metadata json.RawMessage
	metaMap := map[string]any{}
	if len(contentParts) > 0 {
		metaMap["contentParts"] = contentParts
	}
	if len(metaMap) > 0 {
		if metaBytes, err := json.Marshal(metaMap); err == nil {
			metadata = metaBytes
		}
	}

	assistantMsg.Content = fullContent.String()
	assistantMsg.Metadata = metadata
	tc := int32(len(assistantMsg.Content))
	assistantMsg.TokenCount = &tc

	if err := s.convSvc.SaveMessage(ctx, assistantMsg); err != nil {
		slog.Error("failed to update assistant message", "err", err)
	}

	durationMs := int32(time.Since(startTime).Milliseconds())

	if s.usageSvc != nil {
		providerName := ""
		if p := s.providerRouter.Default(); p != nil {
			providerName = p.Name()
		}
		s.usageSvc.RecordAsync(ctx, userID, conv.ID, assistantMsgID,
			providerName, usageAcc, purpose, "web", durationMs)
	}

	go s.generateTitleIfNeeded(conv, req.Message, userID)

	events <- agent.NewEvent(agent.EventStreamEnd, map[string]any{
		"usage": map[string]any{
			"input_tokens":  usageAcc.InputTokens,
			"output_tokens": usageAcc.OutputTokens,
		},
	})
}

func (s *Service) generateTitleIfNeeded(conv *domain.Conversation, firstMessage string, userID uuid.UUID) {
	if conv.Title != nil {
		return
	}

	provider := s.providerRouter.Default()
	if provider == nil {
		return
	}

	prompt := "用10个字以内概括这段对话主题：" + firstMessage
	temp := 0.5
	titleReq := &llm.LlmRequest{
		Messages:    []llm.LlmMessage{{Role: "user", Content: prompt}},
		MaxTokens:   50,
		Temperature: &temp,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := provider.Chat(ctx, titleReq)
	if err != nil {
		slog.Error("failed to generate title", "err", err)
		return
	}

	title := strings.TrimSpace(resp.Content)
	title = strings.ReplaceAll(title, "\"", "")
	title = strings.ReplaceAll(title, "。", "")
	if len(title) > 60 {
		title = title[:60]
	}

	if err := s.convSvc.UpdateTitle(ctx, conv.ID, title); err != nil {
		slog.Error("failed to update title", "err", err)
		return
	}

	slog.Info("generated title", "conversationId", conv.ID, "title", title)
}

func parseStringSlice(v any) []string {
	if names, ok := v.([]string); ok {
		return names
	}
	arr, ok := v.([]any)
	if !ok {
		return []string{}
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok && s != "" {
			result = append(result, s)
		}
	}
	return result
}

func parseStringPointer(v any) *string {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	return &s
}

func (s *Service) Regenerate(ctx context.Context, conversationID, messageID string, userID uuid.UUID) <-chan agent.Event {
	events := make(chan agent.Event, 64)

	go func() {
		defer close(events)

		convID, err := uuid.Parse(conversationID)
		if err != nil {
			events <- agent.ErrorEvent("invalid_id", "无效的会话ID")
			return
		}
		msgID, err := uuid.Parse(messageID)
		if err != nil {
			events <- agent.ErrorEvent("invalid_id", "无效的消息ID")
			return
		}

		conv, err := s.convSvc.GetByIDAndUserID(ctx, convID, userID)
		if err != nil || conv == nil {
			events <- agent.ErrorEvent("forbidden", "会话不存在")
			return
		}

		if s.taskScheduler != nil {
			if n, err := s.taskScheduler.DeleteTasksBySourceMessage(ctx, msgID); err != nil {
				slog.Error("failed to clean up tasks for regenerate", "err", err)
			} else if n > 0 {
				slog.Info("cleaned up tasks from previous generation", "count", n, "messageId", msgID)
			}
		}

		if err := s.convSvc.DeleteMessage(ctx, msgID); err != nil {
			slog.Error("failed to delete message for regenerate", "err", err)
		}

		history, err := s.convSvc.GetMessages(ctx, convID)
		if err != nil {
			events <- agent.ErrorEvent("load_history_failed", "加载历史失败")
			return
		}

		mcpState := &conversation.MCPState{Mode: "inactive", ActivatedTools: []string{}}
		var prevMcpState *conversation.MCPState
		if state, prevState, stateErr := s.convSvc.GetMCPStateWithPrev(ctx, convID, userID); stateErr != nil {
			slog.Warn("failed to load conversation MCP state for regenerate", "conversationId", convID, "err", stateErr)
		} else {
			mcpState = state
			prevMcpState = prevState
		}
		if mcpState.SourceMessageID != nil && *mcpState.SourceMessageID == msgID.String() {
			if prevMcpState != nil {
				mcpState = &conversation.MCPState{
					Mode:            prevMcpState.Mode,
					ActivatedTools:  append([]string(nil), prevMcpState.ActivatedTools...),
					SourceMessageID: prevMcpState.SourceMessageID,
				}
			} else {
				mcpState.Mode = "inactive"
				mcpState.ActivatedTools = []string{}
				mcpState.SourceMessageID = nil
			}
			if err := s.convSvc.SetMCPState(ctx, convID, userID, mcpState); err != nil {
				slog.Warn("failed to reset conversation MCP state for regenerate", "conversationId", convID, "messageId", msgID, "err", err)
			}
		}

		llmReq := s.contextAsm.Assemble(history, conversationID, "", "")

		newMsgID := uuid.New()
		assistantMsg := &domain.Message{
			ID:             newMsgID,
			ConversationID: convID,
			Role:           "assistant",
			Content:        "",
			CreatedAt:      time.Now(),
		}
		if err := s.convSvc.SaveMessage(ctx, assistantMsg); err != nil {
			slog.Error("failed to create regenerated placeholder message", "err", err)
			events <- agent.ErrorEvent("save_failed", "保存消息失败")
			return
		}
		usageAcc := &llm.LlmUsage{}
		startTime := time.Now()

		events <- agent.NewEvent(agent.EventStreamStart, map[string]any{
			"messageId":      newMsgID.String(),
			"conversationId": convID.String(),
		})

		var fullContent strings.Builder
		var contentParts []map[string]any
		toolUseIndex := make(map[string]map[string]any)
		hasTools := len(s.toolRegistry.All()) > 0
		purpose := "chat"

		if hasTools {
			purpose = "chat_with_tools"
			currentMcpState := &conversation.MCPState{
				Mode:            mcpState.Mode,
				ActivatedTools:  append([]string(nil), mcpState.ActivatedTools...),
				SourceMessageID: mcpState.SourceMessageID,
			}
			toolCtx := &tool.ToolContext{
				ConversationID: convID,
				MessageID:      newMsgID,
				UserID:         userID,
			}

			var textAccum strings.Builder
			for ev := range s.agentLoop.Execute(ctx, llmReq, toolCtx, usageAcc, &agent.ExecuteOptions{
				MCPMode:        currentMcpState.Mode,
				ActivatedTools: currentMcpState.ActivatedTools,
			}) {
				switch ev.Type {
				case agent.EventTextDelta:
					if c, ok := ev.Data["content"].(string); ok {
						fullContent.WriteString(c)
						textAccum.WriteString(c)
					}
				case agent.EventToolUseStart:
					if textAccum.Len() > 0 {
						contentParts = append(contentParts, map[string]any{"type": "text", "text": textAccum.String()})
						textAccum.Reset()
					}
					startedAt := time.Now().UnixMilli()
					tc := map[string]any{
						"toolUseId": ev.Data["toolUseId"],
						"toolName":  ev.Data["toolName"],
						"status":    "running",
						"startedAt": startedAt,
					}
					if toolUseID, _ := ev.Data["toolUseId"].(string); toolUseID != "" {
						toolUseIndex[toolUseID] = tc
					}
					contentParts = append(contentParts, map[string]any{"type": "tool_use", "toolCall": tc})
				case agent.EventToolUseInput:
					toolUseID, _ := ev.Data["toolUseId"].(string)
					if tc, ok := toolUseIndex[toolUseID]; ok {
						tc["input"] = ev.Data["input"]
					}
				case agent.EventToolResult:
					toolUseID, _ := ev.Data["toolUseId"].(string)
					status, _ := ev.Data["status"].(string)
					content, _ := ev.Data["content"].(string)
					if mode, ok := ev.Data["mcp_mode"].(string); ok {
						currentMcpState.Mode = mode
						currentMcpState.ActivatedTools = parseStringSlice(ev.Data["activated_tools"])
						if sourceMessageID := parseStringPointer(ev.Data["source_message_id"]); sourceMessageID != nil {
							currentMcpState.SourceMessageID = sourceMessageID
						} else if mode != "active" {
							currentMcpState.SourceMessageID = nil
						}
					}
					if tc, ok := toolUseIndex[toolUseID]; ok {
						tc["status"] = status
						if startedAt, ok := tc["startedAt"].(int64); ok && startedAt > 0 {
							tc["duration"] = time.Now().UnixMilli() - startedAt
						}
					}
					contentParts = append(contentParts, map[string]any{
						"type":      "tool_result",
						"toolUseId": toolUseID,
						"status":    status,
						"content":   content,
					})
				}
				events <- ev
			}

			if textAccum.Len() > 0 {
				contentParts = append(contentParts, map[string]any{"type": "text", "text": textAccum.String()})
			}
			if err := s.convSvc.SetMCPState(ctx, convID, userID, currentMcpState); err != nil {
				slog.Warn("failed to persist conversation MCP state for regenerate", "conversationId", convID, "err", err)
			}
		} else {
			provider := s.providerRouter.Default()
			if provider == nil {
				events <- agent.ErrorEvent("no_provider", "没有可用的LLM提供者")
				return
			}
			chunks, err := provider.StreamChat(ctx, llmReq)
			if err != nil {
				events <- agent.ErrorEvent("stream_error", err.Error())
				return
			}
			for chunk := range chunks {
				if chunk.Content != "" {
					fullContent.WriteString(chunk.Content)
					events <- agent.TextDeltaEvent(chunk.Content)
				}
				if chunk.Usage != nil {
					usageAcc.Add(chunk.Usage)
				}
			}
		}

		assistantMsg.Content = fullContent.String()
		tc := int32(len(assistantMsg.Content))
		assistantMsg.TokenCount = &tc

		// Build metadata
		metaMap := map[string]any{}
		if len(contentParts) > 0 {
			metaMap["contentParts"] = contentParts
		}
		if len(metaMap) > 0 {
			if metaBytes, err := json.Marshal(metaMap); err == nil {
				assistantMsg.Metadata = metaBytes
			}
		}

		persistCtx, persistCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer persistCancel()
		if err := s.convSvc.SaveMessage(persistCtx, assistantMsg); err != nil {
			slog.Error("failed to update regenerated message", "err", err)
		}

		durationMs := int32(time.Since(startTime).Milliseconds())
		if s.usageSvc != nil {
			providerName := ""
			if p := s.providerRouter.Default(); p != nil {
				providerName = p.Name()
			}
			s.usageSvc.RecordAsync(persistCtx, userID, convID, newMsgID,
				providerName, usageAcc, purpose, "web", durationMs)
		}

		events <- agent.NewEvent(agent.EventStreamEnd, map[string]any{
			"usage": map[string]any{
				"input_tokens":  usageAcc.InputTokens,
				"output_tokens": usageAcc.OutputTokens,
			},
		})
	}()

	return events
}
