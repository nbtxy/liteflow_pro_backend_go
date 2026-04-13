package llm

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/liteflow/backend/internal/domain"
)

const (
	ReservedOutputTokens        = 4096
	ToolDefinitionsTokenEstimate = 800
)

type ContextAssembler struct {
	templateEngine *PromptTemplateEngine
	providerRouter *ProviderRouter
	toolDefs       []ToolDefinition
	hasTools       bool
}

func NewContextAssembler(
	templateEngine *PromptTemplateEngine,
	providerRouter *ProviderRouter,
	toolDefs []ToolDefinition,
) *ContextAssembler {
	return &ContextAssembler{
		templateEngine: templateEngine,
		providerRouter: providerRouter,
		toolDefs:       toolDefs,
		hasTools:       len(toolDefs) > 0,
	}
}

func (ca *ContextAssembler) Assemble(history []domain.Message, conversationID string,
	memoryContext string, toolSearchDescription string) *LlmRequest {

	variables := map[string]string{
		"current_time": time.Now().Format(time.RFC3339),
		"user_name":    "Developer",
	}

	modules := []string{
		"base-persona",
		"formatting-rules",
		"conversation-rules",
		"safety-boundaries",
	}

	if ca.hasTools {
		modules = append(modules, "memory-guidance")
	}

	systemPrompt := ca.templateEngine.Assemble(modules, variables)

	if memoryContext != "" {
		systemPrompt += "\n\n" + memoryContext
	}

	systemTokens := estimateTokens(systemPrompt)

	provider := ca.providerRouter.Default()
	maxContext := 64000
	if provider != nil {
		maxContext = provider.MaxContextTokens()
	}

	toolTokens := 0
	if ca.hasTools {
		toolTokens = ToolDefinitionsTokenEstimate
	}
	if toolSearchDescription != "" {
		toolTokens += estimateTokens(toolSearchDescription)
	}
	historyBudget := maxContext - systemTokens - ReservedOutputTokens - toolTokens

	var llmHistory []LlmMessage
	if len(history) > 0 {
		currentTokens := 0
		for i := len(history) - 1; i >= 0; i-- {
			msg := history[i]
			msgTokens := estimateTokens(msg.Content)
			if msg.TokenCount != nil {
				msgTokens = int(*msg.TokenCount)
			}

			if currentTokens+msgTokens > historyBudget {
				if i == len(history)-1 {
					slog.Warn("latest message exceeds budget, including anyway")
				} else {
					break
				}
			}

			llmHistory = append([]LlmMessage{buildLlmMessage(msg)}, llmHistory...)
			currentTokens += msgTokens
		}

		llmHistory = sanitizeToolCallPairs(llmHistory)
	}

	req := &LlmRequest{
		Messages:    llmHistory,
		MaxTokens:   ReservedOutputTokens,
		Temperature: floatPtr(0.0),
		Stream:      true,
	}

	if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
		req.Messages = append([]LlmMessage{{Role: "system", Content: systemPrompt}}, req.Messages...)
	}

	if ca.hasTools {
		defs := ca.toolDefs
		if toolSearchDescription != "" {
			defs = overrideActiveMcpDescription(defs, toolSearchDescription)
		}
		req.Tools = defs
	}

	slog.Info("context assembled",
		"systemPromptTokens", systemTokens,
		"historyMessages", len(llmHistory),
		"historyBudget", historyBudget,
		"toolCount", len(ca.toolDefs),
		"hasMemory", memoryContext != "",
	)

	return req
}

func buildLlmMessage(msg domain.Message) LlmMessage {
	if msg.Role == "tool" && msg.Metadata != nil {
		var meta domain.MessageMetadata
		if err := json.Unmarshal(msg.Metadata, &meta); err == nil {
			return LlmMessage{
				Role:       "tool",
				Content:    msg.Content,
				ToolCallID: meta.ToolCallID,
				Name:       meta.ToolName,
			}
		}
	}

	if msg.Role == "assistant" && msg.Metadata != nil {
		var meta struct {
			ToolCalls []ToolCall `json:"tool_calls"`
		}
		if err := json.Unmarshal(msg.Metadata, &meta); err == nil && len(meta.ToolCalls) > 0 {
			return LlmMessage{
				Role:      "assistant",
				Content:   msg.Content,
				ToolCalls: meta.ToolCalls,
			}
		}
	}

	if msg.Role == "user" && msg.Metadata != nil {
		var meta struct {
			QuotedMessage *struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"quotedMessage"`
			Attachments []struct {
				Type     string `json:"type"`
				URL      string `json:"url"`
				FileName string `json:"fileName"`
			} `json:"attachments"`
		}
		if err := json.Unmarshal(msg.Metadata, &meta); err == nil {
			content := msg.Content
			if meta.QuotedMessage != nil {
				quotedRole := "用户"
				if meta.QuotedMessage.Role == "assistant" {
					quotedRole = "AI"
				}
				content = "[引用" + quotedRole + "消息]\n" + meta.QuotedMessage.Content + "\n\n" + content
			}
			if content != msg.Content {
				return LlmMessage{Role: "user", Content: content}
			}
		}
	}

	return LlmMessage{Role: msg.Role, Content: msg.Content}
}

func sanitizeToolCallPairs(messages []LlmMessage) []LlmMessage {
	respondedIDs := make(map[string]bool)
	requestedIDs := make(map[string]bool)

	for _, msg := range messages {
		if msg.Role == "tool" && msg.ToolCallID != "" {
			respondedIDs[msg.ToolCallID] = true
		}
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				requestedIDs[tc.ID] = true
			}
		}
	}

	var sanitized []LlmMessage
	for _, msg := range messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			allResponded := true
			for _, tc := range msg.ToolCalls {
				if !respondedIDs[tc.ID] {
					allResponded = false
					break
				}
			}
			if !allResponded {
				if msg.Content != "" {
					sanitized = append(sanitized, LlmMessage{Role: "assistant", Content: msg.Content})
				}
				continue
			}
		}

		if msg.Role == "tool" && msg.ToolCallID != "" {
			if !requestedIDs[msg.ToolCallID] {
				continue
			}
		}

		sanitized = append(sanitized, msg)
	}

	return sanitized
}

func overrideActiveMcpDescription(defs []ToolDefinition, description string) []ToolDefinition {
	result := make([]ToolDefinition, len(defs))
	copy(result, defs)
	for i, td := range result {
		if td.Function.Name == "active_mcp_tool" {
			result[i].Function.Description = description
		}
	}
	return result
}

func estimateTokens(text string) int {
	return len(text)
}

func floatPtr(v float64) *float64 {
	return &v
}
