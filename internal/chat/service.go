package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/agent"
	"github.com/liteflow/backend/internal/agent_profile"
	"github.com/liteflow/backend/internal/conversation"
	"github.com/liteflow/backend/internal/domain"
	"github.com/liteflow/backend/internal/llm"
	"github.com/liteflow/backend/internal/memory"
	"github.com/liteflow/backend/internal/task"
	"github.com/liteflow/backend/internal/tool"
	"github.com/liteflow/backend/internal/usage"
	"github.com/liteflow/backend/internal/user"
)

type Service struct {
	providerRouter *llm.ProviderRouter
	contextAsm     *llm.ContextAssembler
	toolRegistry   *tool.Registry
	agentLoop      *agent.AgentLoop
	convSvc        *conversation.Service
	agentSvc       *agent_profile.Service
	userSvc        *user.Service
	memorySvc      *memory.Service
	usageSvc       *usage.Service
	taskScheduler  *task.Scheduler
}

func NewService(
	providerRouter *llm.ProviderRouter,
	contextAsm *llm.ContextAssembler,
	toolRegistry *tool.Registry,
	agentLoop *agent.AgentLoop,
	convSvc *conversation.Service,
	agentSvc *agent_profile.Service,
	userSvc *user.Service,
	memorySvc *memory.Service,
	usageSvc *usage.Service,
) *Service {
	svc := &Service{
		providerRouter: providerRouter,
		contextAsm:     contextAsm,
		toolRegistry:   toolRegistry,
		agentLoop:      agentLoop,
		convSvc:        convSvc,
		agentSvc:       agentSvc,
		userSvc:        userSvc,
		memorySvc:      memorySvc,
		usageSvc:       usageSvc,
	}
	return svc
}

func (s *Service) SetTaskScheduler(ts *task.Scheduler) {
	s.taskScheduler = ts
}

type ChatRequest struct {
	ConversationID string            `json:"conversationId"`
	Message        string            `json:"message"`
	AgentID        string            `json:"agentId,omitempty"`
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

	selectedAgentID := s.resolveRequestedAgentID(req)
	if selectedAgentID == "" {
		if lastAgentMsg, lastErr := s.convSvc.GetLastAgentMessage(ctx, conv.ID); lastErr == nil && lastAgentMsg != nil && lastAgentMsg.AgentID != nil {
			selectedAgentID = *lastAgentMsg.AgentID
		}
	}

	rt := s.newDefaultRuntime()
	if s.agentSvc != nil {
		agentRT, resolveErr := s.agentSvc.Resolve(ctx, selectedAgentID)
		if resolveErr != nil {
			events <- agent.ErrorEvent("invalid_agent", "无效的Agent配置")
			return
		}
		rt = agentRT
	}
	s.applyUserToolAccess(ctx, rt, userID)
	var resolvedAgentID *string
	if rt != nil && strings.TrimSpace(rt.AgentID) != "" {
		aid := rt.AgentID
		resolvedAgentID = &aid
	}

	userMsg := &domain.Message{
		ConversationID: conv.ID,
		Role:           "user",
		SenderType:     "user",
		AgentID:        resolvedAgentID,
		CreatedAt:      time.Now(),
	}
	if parts, err := json.Marshal([]map[string]any{{"type": "text", "text": req.Message}}); err == nil {
		userMsg.ContentParts = parts
	}
	tokenCount := int32(len(req.Message))
	userMsg.TokenCount = &tokenCount

	userMeta := map[string]any{}
	if len(req.Attachments) > 0 {
		userMeta["attachments"] = req.Attachments
	}
	if len(req.QuotedMessage) > 0 {
		userMeta["quotedMessage"] = req.QuotedMessage
	}
	if len(userMeta) > 0 {
		if metaBytes, err := json.Marshal(userMeta); err == nil {
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

	memoryContext := s.buildMemoryContext(ctx, userID)
	llmReq := s.contextAsm.Assemble(ctx, history, conv.ID.String(), memoryContext, "", &llm.AssembleOptions{
		Provider:             rt.Provider,
		ToolDefs:             rt.ToolDefs,
		PromptModules:        rt.PromptModules,
		SystemPromptOverride: rt.SystemPromptOverride,
		UserName:             s.resolvePromptUserName(ctx, userID),
		Temperature:          rt.Temperature,
		MaxTokens:            rt.MaxTokens,
		Model:                rt.Model,
	})

	assistantMsgID := uuid.New()
	assistantMsg := &domain.Message{
		ID:             assistantMsgID,
		ConversationID: conv.ID,
		Role:           "assistant",
		SenderType:     "agent",
		AgentID:        resolvedAgentID,
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

	var contentParts []map[string]any

	hasTools := rt != nil && len(rt.EnabledToolSet) > 0
	purpose := "chat"
	if hasTools {
		purpose = "chat_with_tools"
	}

	provider := s.resolveRuntimeProvider(rt)
	if provider == nil {
		events <- agent.ErrorEvent("no_provider", "没有可用的LLM提供者")
		return
	}

	currentMcpState := &conversation.MCPState{
		Mode:           "inactive",
		ActivatedTools: []string{},
	}
	if hasTools {
		currentMcpState = &conversation.MCPState{
			Mode:            mcpState.Mode,
			ActivatedTools:  append([]string(nil), mcpState.ActivatedTools...),
			SourceMessageID: mcpState.SourceMessageID,
		}
	}
	toolCtx := &tool.ToolContext{
		ConversationID: conv.ID,
		MessageID:      assistantMsgID,
		UserID:         userID,
	}
	runResult := s.streamAgentEvents(ctx, llmReq, toolCtx, usageAcc, rt, currentMcpState, events)
	contentParts = runResult.contentParts
	if hasTools {
		if err := s.convSvc.SetMCPState(ctx, conv.ID, userID, currentMcpState); err != nil {
			slog.Warn("failed to persist conversation MCP state", "conversationId", conv.ID, "err", err)
		}
	}

	var metadata json.RawMessage
	metaMap := map[string]any{}
	applyStreamErrorFallback(&contentParts, runResult, metaMap)
	contentParts = settleRunningToolCallsForPersistence(contentParts, "error")
	normalizedParts := normalizeContentParts(contentParts)
	if parts, err := json.Marshal(normalizedParts); err == nil {
		assistantMsg.ContentParts = parts
	}
	if len(metaMap) > 0 {
		if metaBytes, err := json.Marshal(metaMap); err == nil {
			metadata = metaBytes
		}
	}

	assistantMsg.Metadata = metadata
	tc := int32(len(extractTextFromContentParts(normalizedParts)))
	assistantMsg.TokenCount = &tc

	if err := s.convSvc.SaveMessage(ctx, assistantMsg); err != nil {
		slog.Error("failed to update assistant message", "err", err)
	}

	durationMs := int32(time.Since(startTime).Milliseconds())

	if s.usageSvc != nil {
		providerName := ""
		if rt != nil && rt.Provider != nil {
			providerName = rt.Provider.Name()
		} else if p := s.providerRouter.Default(); p != nil {
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
		MaxTokens:   200,
		Temperature: &temp,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := provider.Chat(ctx, titleReq)
	if err != nil {
		slog.Error("failed to generate title", "err", err)
		return
	}

	rawTitle := resp.Content
	title := strings.TrimSpace(rawTitle)
	title = strings.Trim(title, "\"'`")
	title = strings.TrimSuffix(title, "。")
	title = strings.TrimSuffix(title, ".")
	title = strings.TrimSpace(title)
	if len(title) > 60 {
		title = title[:60]
	}

	if title == "" {
		// Fallback to first 20 chars of the user message so the conversation
		// gets a non-empty title even if the LLM returns blank/punctuation.
		fallback := strings.TrimSpace(firstMessage)
		runes := []rune(fallback)
		if len(runes) > 20 {
			fallback = string(runes[:20])
		}
		title = fallback
	}

	if title == "" {
		slog.Warn("title generation produced empty result; skipping update",
			"conversationId", conv.ID, "rawResponse", rawTitle)
		return
	}

	if err := s.convSvc.UpdateTitle(ctx, conv.ID, title); err != nil {
		slog.Error("failed to update title", "err", err)
		return
	}

	slog.Info("generated title", "conversationId", conv.ID, "title", title, "raw", rawTitle)
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

func (s *Service) resolveRequestedAgentID(req ChatRequest) string {
	if strings.TrimSpace(req.AgentID) != "" {
		return strings.TrimSpace(req.AgentID)
	}
	return ""
}

func (s *Service) resolvePromptUserName(ctx context.Context, userID uuid.UUID) string {
	if s.userSvc == nil || userID == uuid.Nil {
		return "Developer"
	}
	u, err := s.userSvc.GetByID(ctx, userID)
	if err != nil || u == nil {
		return "Developer"
	}
	if u.Name != nil {
		if name := strings.TrimSpace(*u.Name); name != "" {
			return name
		}
	}
	if phone := strings.TrimSpace(u.Phone); phone != "" {
		return phone
	}
	return "Developer"
}

func (s *Service) buildMemoryContext(ctx context.Context, userID uuid.UUID) string {
	if s == nil || s.memorySvc == nil || userID == uuid.Nil {
		return ""
	}
	memories, err := s.memorySvc.GetFormattedMemories(ctx, userID)
	if err != nil {
		slog.Warn("failed to load memory context", "userId", userID, "err", err)
		return ""
	}
	return strings.TrimSpace(memories)
}

func (s *Service) newDefaultRuntime() *agent_profile.AgentRuntime {
	tools := make(map[string]tool.Tool)
	for _, t := range s.toolRegistry.All() {
		tools[t.Name()] = t
	}
	return &agent_profile.AgentRuntime{
		AgentName:      "Assistant",
		Provider:       s.providerRouter.Default(),
		ToolDefs:       s.toolRegistry.BuildToolDefinitions(),
		EnabledToolSet: tools,
	}
}

func (s *Service) applyUserToolAccess(ctx context.Context, rt *agent_profile.AgentRuntime, userID uuid.UUID) {
	if rt == nil || len(rt.EnabledToolSet) == 0 {
		return
	}
	userPhone := ""
	loadedPhone := false
	filtered := make(map[string]tool.Tool, len(rt.EnabledToolSet))
	for name, t := range rt.EnabledToolSet {
		if scoped, ok := t.(tool.UserScopedTool); ok && !scoped.IsEnabledForUser(userID) {
			continue
		}
		if scopedByPhone, ok := t.(tool.PhoneScopedTool); ok {
			if !loadedPhone {
				userPhone = s.resolveUserPhone(ctx, userID)
				loadedPhone = true
			}
			if !scopedByPhone.IsEnabledForPhone(userPhone) {
				continue
			}
		}
		filtered[name] = t
	}
	rt.EnabledToolSet = filtered
	rt.ToolDefs = buildToolDefsFromPool(filtered)
}

func (s *Service) resolveUserPhone(ctx context.Context, userID uuid.UUID) string {
	if s == nil || s.userSvc == nil || userID == uuid.Nil {
		return ""
	}
	u, err := s.userSvc.GetByID(ctx, userID)
	if err != nil || u == nil {
		return ""
	}
	return strings.TrimSpace(u.Phone)
}

func buildToolDefsFromPool(pool map[string]tool.Tool) []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(pool))
	for _, t := range pool {
		defs = append(defs, llm.ToolDefinition{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.InputSchema(),
			},
		})
	}
	return defs
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

		var selectedAgentID string
		if oldMsg, oldErr := s.convSvc.GetMessageByID(ctx, msgID); oldErr == nil && oldMsg != nil && oldMsg.AgentID != nil {
			selectedAgentID = *oldMsg.AgentID
		}
		if selectedAgentID == "" {
			if lastAgentMsg, lastErr := s.convSvc.GetLastAgentMessage(ctx, convID); lastErr == nil && lastAgentMsg != nil && lastAgentMsg.AgentID != nil {
				selectedAgentID = *lastAgentMsg.AgentID
			}
		}

		rt := s.newDefaultRuntime()
		if s.agentSvc != nil {
			agentRT, resolveErr := s.agentSvc.Resolve(ctx, selectedAgentID)
			if resolveErr != nil {
				events <- agent.ErrorEvent("invalid_agent", "无效的Agent配置")
				return
			}
			rt = agentRT
		}
		s.applyUserToolAccess(ctx, rt, userID)
		var resolvedAgentID *string
		if rt != nil && strings.TrimSpace(rt.AgentID) != "" {
			aid := rt.AgentID
			resolvedAgentID = &aid
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

		memoryContext := s.buildMemoryContext(ctx, userID)
		llmReq := s.contextAsm.Assemble(ctx, history, conversationID, memoryContext, "", &llm.AssembleOptions{
			Provider:             rt.Provider,
			ToolDefs:             rt.ToolDefs,
			PromptModules:        rt.PromptModules,
			SystemPromptOverride: rt.SystemPromptOverride,
			UserName:             s.resolvePromptUserName(ctx, userID),
			Temperature:          rt.Temperature,
			MaxTokens:            rt.MaxTokens,
			Model:                rt.Model,
		})

		newMsgID := uuid.New()
		assistantMsg := &domain.Message{
			ID:             newMsgID,
			ConversationID: convID,
			Role:           "assistant",
			SenderType:     "agent",
			AgentID:        resolvedAgentID,
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

		var contentParts []map[string]any
		hasTools := rt != nil && len(rt.EnabledToolSet) > 0
		purpose := "chat"
		if hasTools {
			purpose = "chat_with_tools"
		}

		provider := s.resolveRuntimeProvider(rt)
		if provider == nil {
			events <- agent.ErrorEvent("no_provider", "没有可用的LLM提供者")
			return
		}

		currentMcpState := &conversation.MCPState{
			Mode:           "inactive",
			ActivatedTools: []string{},
		}
		if hasTools {
			currentMcpState = &conversation.MCPState{
				Mode:            mcpState.Mode,
				ActivatedTools:  append([]string(nil), mcpState.ActivatedTools...),
				SourceMessageID: mcpState.SourceMessageID,
			}
		}
		toolCtx := &tool.ToolContext{
			ConversationID: convID,
			MessageID:      newMsgID,
			UserID:         userID,
		}
		runResult := s.streamAgentEvents(ctx, llmReq, toolCtx, usageAcc, rt, currentMcpState, events)
		contentParts = runResult.contentParts
		if hasTools {
			if err := s.convSvc.SetMCPState(ctx, convID, userID, currentMcpState); err != nil {
				slog.Warn("failed to persist conversation MCP state for regenerate", "conversationId", convID, "err", err)
			}
		}

		// Build metadata
		metaMap := map[string]any{}
		applyStreamErrorFallback(&contentParts, runResult, metaMap)
		contentParts = settleRunningToolCallsForPersistence(contentParts, "error")
		normalizedParts := normalizeContentParts(contentParts)
		if parts, err := json.Marshal(normalizedParts); err == nil {
			assistantMsg.ContentParts = parts
		}
		tc := int32(len(extractTextFromContentParts(normalizedParts)))
		assistantMsg.TokenCount = &tc
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
			if rt != nil && rt.Provider != nil {
				providerName = rt.Provider.Name()
			} else if p := s.providerRouter.Default(); p != nil {
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

type agentStreamRunResult struct {
	contentParts []map[string]any
	errorCode    string
	errorMessage string
}

func (s *Service) resolveRuntimeProvider(rt *agent_profile.AgentRuntime) llm.Provider {
	provider := s.providerRouter.Default()
	if rt != nil && rt.Provider != nil {
		provider = rt.Provider
	}
	return provider
}

func (s *Service) resolveRuntimeToolExecution(rt *agent_profile.AgentRuntime) (map[string]tool.Tool, []string) {
	if rt == nil {
		return nil, nil
	}
	return rt.EnabledToolSet, rt.EnabledMcpChannelNames
}

func (s *Service) streamAgentEvents(
	ctx context.Context,
	llmReq *llm.LlmRequest,
	toolCtx *tool.ToolContext,
	usageAcc *llm.LlmUsage,
	rt *agent_profile.AgentRuntime,
	currentMcpState *conversation.MCPState,
	events chan<- agent.Event,
) agentStreamRunResult {
	toolPool, allowedMcpChannelNames := s.resolveRuntimeToolExecution(rt)
	toolUseIndex := make(map[string]map[string]any)
	var textAccum strings.Builder
	contentParts := make([]map[string]any, 0, 16)
	var streamErrorCode string
	var streamErrorMessage string

	for ev := range s.agentLoop.Execute(ctx, llmReq, toolCtx, usageAcc, &agent.ExecuteOptions{
		MCPMode:                currentMcpState.Mode,
		ActivatedTools:         currentMcpState.ActivatedTools,
		ToolPool:               toolPool,
		AllowedMcpChannelNames: allowedMcpChannelNames,
		AgentRuntime:           rt,
	}) {
		switch ev.Type {
		case agent.EventTextDelta:
			if content, ok := ev.Data["content"].(string); ok {
				textAccum.WriteString(content)
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
		case agent.EventToolUseInputDelta:
			toolUseID, _ := ev.Data["toolUseId"].(string)
			inputDelta, _ := ev.Data["input_delta"].(string)
			if tc, ok := toolUseIndex[toolUseID]; ok && inputDelta != "" {
				prev, _ := tc["input"].(string)
				tc["input"] = prev + inputDelta
			}
		case agent.EventToolResult:
			toolUseID, _ := ev.Data["toolUseId"].(string)
			status, _ := ev.Data["status"].(string)
			content, _ := ev.Data["content"].(string)
			artifactID := strings.TrimSpace(fmt.Sprint(ev.Data["artifactId"]))
			if artifactID == "<nil>" {
				artifactID = ""
			}
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
			part := map[string]any{
				"type":      "tool_result",
				"toolUseId": toolUseID,
				"status":    status,
				"content":   content,
			}
			if strings.TrimSpace(artifactID) != "" {
				part["artifactId"] = artifactID
			}
			contentParts = append(contentParts, part)
		case agent.EventToolProgress:
			toolUseID, _ := ev.Data["toolUseId"].(string)
			toolName, _ := ev.Data["toolName"].(string)
			progress, _ := ev.Data["content"].(string)
			if progress == "" {
				break
			}
			if toolUseID != "" {
				if tc, ok := toolUseIndex[toolUseID]; ok {
					tc["progress"] = progress
					break
				}
			}
			for i := len(contentParts) - 1; i >= 0; i-- {
				part := contentParts[i]
				if mapString(part, "type") != "tool_use" {
					continue
				}
				toolCall, ok := part["toolCall"].(map[string]any)
				if !ok {
					continue
				}
				if mapString(toolCall, "status") != "running" {
					continue
				}
				if toolName != "" && mapString(toolCall, "toolName") != toolName {
					continue
				}
				toolCall["progress"] = progress
				break
			}
		case agent.EventError:
			if code, ok := ev.Data["code"].(string); ok {
				streamErrorCode = code
			}
			if msg, ok := ev.Data["message"].(string); ok {
				streamErrorMessage = msg
			}
		}
		events <- ev
	}

	if textAccum.Len() > 0 {
		contentParts = append(contentParts, map[string]any{"type": "text", "text": textAccum.String()})
	}

	return agentStreamRunResult{
		contentParts: contentParts,
		errorCode:    streamErrorCode,
		errorMessage: streamErrorMessage,
	}
}

func applyStreamErrorFallback(
	contentParts *[]map[string]any,
	runResult agentStreamRunResult,
	metaMap map[string]any,
) {
	if runResult.errorMessage == "" {
		return
	}

	warning := "⚠️ " + runResult.errorMessage
	current := extractTextFromContentParts(*contentParts)

	*contentParts = append(*contentParts, map[string]any{
		"type": "text",
		"text": func() string {
			if strings.TrimSpace(current) == "" {
				return warning
			}
			return "\n\n" + warning
		}(),
	})

	metaMap["error"] = map[string]any{
		"code":    runResult.errorCode,
		"message": runResult.errorMessage,
	}
}

func extractTextFromContentParts(parts []map[string]any) string {
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		if mapString(part, "type") != "text" {
			continue
		}
		b.WriteString(mapString(part, "text"))
	}
	return b.String()
}

func settleRunningToolCallsForPersistence(parts []map[string]any, fallbackStatus string) []map[string]any {
	if len(parts) == 0 {
		return parts
	}

	statusByToolUseID := make(map[string]string, 8)
	for _, part := range parts {
		if mapString(part, "type") != "tool_result" {
			continue
		}
		toolUseID := mapString(part, "toolUseId")
		status := mapString(part, "status")
		if toolUseID == "" || status == "" {
			continue
		}
		statusByToolUseID[toolUseID] = status
	}

	nowMs := time.Now().UnixMilli()
	for _, part := range parts {
		if mapString(part, "type") != "tool_use" {
			continue
		}
		toolCall, ok := part["toolCall"].(map[string]any)
		if !ok || toolCall == nil {
			continue
		}

		toolUseID := mapString(toolCall, "toolUseId")
		if toolUseID == "" {
			continue
		}

		status := mapString(toolCall, "status")
		if status == "running" {
			if settled := statusByToolUseID[toolUseID]; settled != "" {
				toolCall["status"] = settled
				status = settled
			} else if fallbackStatus != "" {
				toolCall["status"] = fallbackStatus
				status = fallbackStatus
			}
		}

		if status != "running" {
			if _, ok := toolCall["duration"]; !ok {
				if startedAt, ok := toInt64(toolCall["startedAt"]); ok && startedAt > 0 {
					duration := nowMs - startedAt
					if duration < 0 {
						duration = 0
					}
					toolCall["duration"] = duration
				}
			}
		}
	}

	return parts
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case float64:
		return int64(n), true
	case float32:
		return int64(n), true
	default:
		return 0, false
	}
}

func normalizeContentParts(parts []map[string]any) []map[string]any {
	if len(parts) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		typ := mapString(part, "type")
		if typ == "" {
			continue
		}
		switch typ {
		case "text":
			text := mapString(part, "text")
			if text == "" {
				continue
			}
			if len(out) > 0 && mapString(out[len(out)-1], "type") == "text" {
				out[len(out)-1]["text"] = mapString(out[len(out)-1], "text") + text
				continue
			}
			out = append(out, map[string]any{
				"type": "text",
				"text": text,
			})
		case "tool_use", "tool_result":
			normalized := map[string]any{"type": typ}
			for k, v := range part {
				if k == "type" {
					continue
				}
				normalized[k] = v
			}
			out = append(out, normalized)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mapString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	if v, ok := data[key].(string); ok {
		return v
	}
	return ""
}
