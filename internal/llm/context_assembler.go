package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/liteflow/backend/internal/domain"
	"github.com/liteflow/backend/internal/platform/storage"
)

const (
	ReservedOutputTokens         = 4096
	ToolDefinitionsTokenEstimate = 800
	ImageTokenEstimate           = 1000 // conservative per-image budget
	CompactTriggerRatio          = 0.85 // trigger proactive compaction above this fraction of max context
	MinTruncatedUserMessage      = 100  // minimum tokens kept when hard-truncating a latest user message
)

type ContextAssembler struct {
	templateEngine *PromptTemplateEngine
	providerRouter *ProviderRouter
	storageSvc     storage.Service
	ossLinkSvc     storage.Service
	toolDefs       []ToolDefinition
	hasTools       bool
	compactor      *Compactor
}

type AssembleOptions struct {
	Provider             Provider
	ToolDefs             []ToolDefinition
	PromptModules        []string
	SystemPromptOverride string
	UserName             string
	Temperature          *float64
	MaxTokens            int
	Model                string
}

func NewContextAssembler(
	templateEngine *PromptTemplateEngine,
	providerRouter *ProviderRouter,
	storageSvc storage.Service,
	ossLinkSvc storage.Service,
	toolDefs []ToolDefinition,
	compactor *Compactor,
) *ContextAssembler {
	return &ContextAssembler{
		templateEngine: templateEngine,
		providerRouter: providerRouter,
		storageSvc:     storageSvc,
		ossLinkSvc:     ossLinkSvc,
		toolDefs:       toolDefs,
		hasTools:       len(toolDefs) > 0,
		compactor:      compactor,
	}
}

func (ca *ContextAssembler) Assemble(ctx context.Context, history []domain.Message, conversationID string,
	memoryContext string, toolSearchDescription string, opts *AssembleOptions) *LlmRequest {

	selectedProvider := ca.providerRouter.Default()
	if opts != nil && opts.Provider != nil {
		selectedProvider = opts.Provider
	}

	defs := ca.toolDefs
	if opts != nil && opts.ToolDefs != nil {
		defs = opts.ToolDefs
	}
	hasTools := len(defs) > 0
	hasMemoryManageTool := hasToolDefinition(defs, "memory_manage")

	userName := "Developer"
	if opts != nil && strings.TrimSpace(opts.UserName) != "" {
		userName = strings.TrimSpace(opts.UserName)
	}

	variables := map[string]string{
		"current_time": time.Now().Format(time.RFC3339),
		"user_name":    userName,
	}

	modules := []string{
		"base-persona",
		"runtime-context",
		"formatting-rules",
		"conversation-rules",
		"safety-boundaries",
	}

	if opts != nil && opts.PromptModules != nil {
		modules = opts.PromptModules
	}

	if hasMemoryManageTool {
		modules = append(modules, "memory-guidance")
	}

	systemPrompt := ca.templateEngine.Assemble(modules, variables)
	if opts != nil && strings.TrimSpace(opts.SystemPromptOverride) != "" {
		systemPrompt += "\n\n" + strings.TrimSpace(opts.SystemPromptOverride)
	}

	if memoryContext != "" {
		systemPrompt += "\n\n" + memoryContext
	}

	systemTokens := estimateTokens(systemPrompt)

	maxContext := 64000
	visionSupported := false
	if selectedProvider != nil {
		maxContext = selectedProvider.MaxContextTokens()
		visionSupported = selectedProvider.SupportsVision()
	}

	toolTokens := 0
	if hasTools {
		toolTokens = estimateToolDefinitionsTokens(defs)
	}
	if toolSearchDescription != "" {
		toolTokens += estimateTokens(toolSearchDescription)
	}
	historyBudget := maxContext - systemTokens - ReservedOutputTokens - toolTokens

	var llmHistory []LlmMessage
	historyTokens := 0
	if len(history) > 0 {
		currentTokens := 0
		for i := len(history) - 1; i >= 0; i-- {
			msg := history[i]
			msgTokens := extractAllMessageTokens(msg)
			if msg.TokenCount != nil {
				msgTokens = int(*msg.TokenCount)
			}

			overBudget := currentTokens+msgTokens > historyBudget
			if overBudget && i != len(history)-1 {
				break
			}

			expanded := buildLlmMessages(msg, conversationID, visionSupported, ca.storageSvc, ca.ossLinkSvc)

			if overBudget && msg.Role == "user" {
				allowed := historyBudget - currentTokens
				if allowed < MinTruncatedUserMessage {
					allowed = MinTruncatedUserMessage
				}
				for j := range expanded {
					if expanded[j].Content != "" {
						expanded[j].Content = hardTruncateText(expanded[j].Content, allowed)
					}
				}
				slog.Warn("hard-truncated latest user message to fit budget",
					"originalTokens", msgTokens, "allowedTokens", allowed)
				msgTokens = allowed
			} else if overBudget {
				slog.Warn("latest message exceeds budget, including anyway",
					"role", msg.Role, "msgTokens", msgTokens)
			}

			llmHistory = append(expanded, llmHistory...)
			currentTokens += msgTokens
			if overBudget {
				break
			}
		}

		llmHistory = sanitizeToolCallPairs(llmHistory)
		historyTokens = currentTokens
	}

	req := &LlmRequest{
		Messages:    llmHistory,
		MaxTokens:   ReservedOutputTokens,
		Temperature: floatPtr(0.0),
		Stream:      true,
	}
	if opts != nil {
		if opts.MaxTokens > 0 {
			req.MaxTokens = opts.MaxTokens
		}
		if opts.Temperature != nil {
			req.Temperature = opts.Temperature
		}
		if strings.TrimSpace(opts.Model) != "" {
			req.Model = strings.TrimSpace(opts.Model)
		}
	}

	if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
		req.Messages = append([]LlmMessage{{Role: "system", Content: systemPrompt}}, req.Messages...)
	}

	if hasTools {
		if toolSearchDescription != "" {
			defs = overrideActiveMcpDescription(defs, toolSearchDescription)
		}
		req.Tools = defs
	}

	estimatedTotal := systemTokens + historyTokens + toolTokens
	slog.Info("context assembled",
		"systemPromptTokens", systemTokens,
		"historyMessages", len(llmHistory),
		"historyBudget", historyBudget,
		"estimatedTotalTokens", estimatedTotal,
		"maxContext", maxContext,
		"toolCount", len(defs),
		"hasMemoryManageTool", hasMemoryManageTool,
		"hasMemory", memoryContext != "",
	)

	if ca.compactor != nil && estimatedTotal > int(float64(maxContext)*CompactTriggerRatio) {
		slog.Info("proactive compaction triggered",
			"estimatedTotal", estimatedTotal,
			"threshold", int(float64(maxContext)*CompactTriggerRatio),
		)
		compacted, cErr := ca.compactor.Compact(ctx, req.Messages, req.Tools, CompactOptions{KeepTailTurns: 3})
		if cErr != nil {
			slog.Warn("proactive compaction failed, sending original", "err", cErr)
		} else {
			req.Messages = compacted
		}
	}

	return req
}

func buildLlmMessages(
	msg domain.Message,
	conversationID string,
	visionSupported bool,
	storageSvc storage.Service,
	ossLinkSvc storage.Service,
) []LlmMessage {
	if msg.Role == "tool" && msg.Metadata != nil {
		var meta domain.MessageMetadata
		if err := json.Unmarshal(msg.Metadata, &meta); err == nil {
			return []LlmMessage{{
				Role:       "tool",
				Content:    extractMessageText(msg),
				ToolCallID: meta.ToolCallID,
				Name:       meta.ToolName,
			}}
		}
	}

	if msg.Role == "assistant" && len(msg.ContentParts) > 0 {
		var parts []assistantContentPart
		if err := json.Unmarshal(msg.ContentParts, &parts); err == nil && len(parts) > 0 {
			intermediate := buildIntermediateMessagesFromContentParts(parts)
			if len(intermediate) > 0 {
				result := make([]LlmMessage, 0, len(intermediate)+1)
				result = append(result, intermediate...)
				if assistantText := extractMessageText(msg); assistantText != "" {
					result = append(result, LlmMessage{Role: "assistant", Content: assistantText})
				}
				return result
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
				Name     string `json:"name"`
			} `json:"attachments"`
		}
		if err := json.Unmarshal(msg.Metadata, &meta); err == nil {
			baseText := extractMessageText(msg)
			content := baseText
			if meta.QuotedMessage != nil {
				quotedRole := "用户"
				if meta.QuotedMessage.Role == "assistant" {
					quotedRole = "AI"
				}
				content = "[引用" + quotedRole + "消息]\n" + meta.QuotedMessage.Content + "\n\n" + content
			}
			if len(meta.Attachments) > 0 {
				textParts := make([]string, 0, len(meta.Attachments))
				images := make([]ImageContent, 0, len(meta.Attachments))

				for _, a := range meta.Attachments {
					fileName := a.FileName
					if fileName == "" {
						fileName = a.Name
					}
					if fileName == "" {
						fileName = "file"
					}

					// Only generate URL when model can actually consume image URLs.
					if a.Type == "image" && visionSupported {
						publicURL := resolveAttachmentURL(conversationID, a.URL, fileName, storageSvc, ossLinkSvc)
						if publicURL != "" {
							images = append(images, ImageContent{
								Type:     "image_url",
								ImageURL: &ImageURL{URL: publicURL},
							})
							continue
						}
					}

					label := "附件"
					if a.Type == "image" {
						label = "图片附件"
					}
					fileInfo := fmt.Sprintf("%s: %s", label, fileName)
					if a.Type != "" {
						fileInfo += fmt.Sprintf(" (type=%s)", a.Type)
					}
					if strings.TrimSpace(a.URL) != "" {
						fileInfo += fmt.Sprintf(" (path=%s)", a.URL)
					}
					textParts = append(textParts, fileInfo)
				}

				combinedContent := content
				if len(textParts) > 0 {
					if combinedContent != "" {
						combinedContent += "\n"
					}
					combinedContent += strings.Join(textParts, "\n")
				}

				if len(images) > 0 || combinedContent != baseText {
					return []LlmMessage{{Role: "user", Content: combinedContent, Images: images}}
				}
			}

			if content != baseText {
				return []LlmMessage{{Role: "user", Content: content}}
			}
		}
	}

	if msg.Role == "user" {
		return []LlmMessage{{Role: msg.Role, Content: extractMessageText(msg)}}
	}
	return []LlmMessage{{Role: msg.Role, Content: extractMessageText(msg)}}
}

type assistantContentPart struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ToolUseID string `json:"toolUseId,omitempty"`
	Content   string `json:"content,omitempty"`
	Status    string `json:"status,omitempty"`
	ToolCall  *struct {
		ToolUseID string `json:"toolUseId"`
		ToolName  string `json:"toolName"`
		Input     any    `json:"input,omitempty"`
	} `json:"toolCall,omitempty"`
}

func extractMessageText(msg domain.Message) string {
	if len(msg.ContentParts) == 0 {
		return ""
	}
	var parts []assistantContentPart
	if err := json.Unmarshal(msg.ContentParts, &parts); err != nil {
		return ""
	}
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		if part.Type == "text" {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

func buildIntermediateMessagesFromContentParts(parts []assistantContentPart) []LlmMessage {
	result := make([]LlmMessage, 0, len(parts))
	toolNameByID := make(map[string]string, 8)
	var pendingText strings.Builder
	pendingToolCalls := make([]ToolCall, 0, 2)

	flushAssistant := func() {
		if pendingText.Len() == 0 && len(pendingToolCalls) == 0 {
			return
		}
		msg := LlmMessage{
			Role:    "assistant",
			Content: pendingText.String(),
		}
		if len(pendingToolCalls) > 0 {
			msg.ToolCalls = append([]ToolCall(nil), pendingToolCalls...)
		}
		result = append(result, msg)
		pendingText.Reset()
		pendingToolCalls = pendingToolCalls[:0]
	}

	for _, part := range parts {
		switch part.Type {
		case "text":
			pendingText.WriteString(part.Text)
		case "tool_use":
			if part.ToolCall == nil || part.ToolCall.ToolUseID == "" {
				continue
			}

			arguments := "{}"
			if part.ToolCall.Input != nil {
				if b, err := json.Marshal(part.ToolCall.Input); err == nil && len(b) > 0 {
					arguments = string(b)
				}
			}
			if !isValidToolArgumentsJSON(arguments) {
				arguments = "{}"
			}

			toolUseID := part.ToolCall.ToolUseID
			toolName := part.ToolCall.ToolName
			if toolName != "" {
				toolNameByID[toolUseID] = toolName
			}

			pendingToolCalls = append(pendingToolCalls, ToolCall{
				ID:   toolUseID,
				Type: "function",
				Function: ToolCallFunc{
					Name:      toolName,
					Arguments: arguments,
				},
			})
		case "tool_result":
			toolUseID := part.ToolUseID
			if toolUseID == "" {
				continue
			}
			flushAssistant()
			result = append(result, LlmMessage{
				Role:       "tool",
				Content:    part.Content,
				ToolCallID: toolUseID,
				Name:       toolNameByID[toolUseID],
			})
		}
	}

	flushAssistant()

	return result
}

func sanitizeToolCallPairs(messages []LlmMessage) []LlmMessage {
	var sanitized []LlmMessage
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			validToolCalls := make([]ToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				if !isValidToolArgumentsJSON(tc.Function.Arguments) {
					slog.Warn("dropping assistant tool call with invalid arguments JSON",
						"toolCallId", tc.ID,
						"toolName", tc.Function.Name)
					continue
				}
				validToolCalls = append(validToolCalls, tc)
			}
			if len(validToolCalls) == 0 {
				if msg.Content != "" {
					sanitized = append(sanitized, LlmMessage{Role: "assistant", Content: msg.Content})
				}
				continue
			}

			respondedIDs := make(map[string]bool, len(validToolCalls))
			j := i + 1
			for ; j < len(messages); j++ {
				next := messages[j]
				if next.Role != "tool" || next.ToolCallID == "" {
					break
				}
				respondedIDs[next.ToolCallID] = true
			}

			allResponded := true
			for _, tc := range validToolCalls {
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

			msg.ToolCalls = validToolCalls
			sanitized = append(sanitized, msg)

			requestedIDs := make(map[string]bool, len(validToolCalls))
			for _, tc := range validToolCalls {
				requestedIDs[tc.ID] = true
			}
			for k := i + 1; k < j; k++ {
				toolMsg := messages[k]
				if requestedIDs[toolMsg.ToolCallID] {
					sanitized = append(sanitized, toolMsg)
				}
			}
			i = j - 1
			continue
		}

		if msg.Role == "tool" {
			// Keep tool messages only when consumed as the contiguous response
			// block immediately following their assistant tool_calls message.
			continue
		}

		sanitized = append(sanitized, msg)
	}

	return sanitized
}

func isValidToolArgumentsJSON(arguments string) bool {
	if strings.TrimSpace(arguments) == "" {
		return false
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err != nil {
		return false
	}
	return true
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

func resolveAttachmentURL(
	conversationID,
	rawURL,
	fileName string,
	storageSvc storage.Service,
	ossLinkSvc storage.Service,
) string {
	if rawURL != "" && (strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://")) {
		return rawURL
	}

	if conversationID == "" {
		return rawURL
	}

	if strings.HasPrefix(rawURL, "data:") {
		uploadedPath, err := uploadDataURLToStorage(conversationID, rawURL, fileName, ossLinkSvc)
		if err != nil && storageSvc != nil {
			uploadedPath, err = uploadDataURLToStorage(conversationID, rawURL, fileName, storageSvc)
		}
		if err != nil {
			slog.Warn("failed to upload data URL attachment", "err", err)
			return rawURL
		}
		linkSvc := pickLinkService(ossLinkSvc, storageSvc)
		if linkSvc == nil {
			return rawURL
		}
		url, err := linkSvc.GeneratePresignedURL(context.Background(), conversationID, uploadedPath, "GET", 60)
		if err != nil {
			slog.Warn("failed to generate presigned URL", "path", uploadedPath, "err", err)
			return rawURL
		}
		return url
	}

	switch {
	case rawURL == "" && fileName != "":
		rawURL = "uploads/" + fileName
	case strings.HasPrefix(rawURL, "/uploads/"):
		rawURL = strings.TrimPrefix(rawURL, "/")
	}

	if strings.HasPrefix(rawURL, "uploads/") {
		if ossLinkSvc != nil && storageSvc != nil {
			publicURL, err := mirrorLocalFileToOSSAndSign(conversationID, rawURL, fileName, storageSvc, ossLinkSvc)
			if err == nil && publicURL != "" {
				return publicURL
			}
			if err != nil {
				slog.Warn("failed to mirror attachment to OSS", "path", rawURL, "err", err)
			}
		}
		if storageSvc == nil {
			return rawURL
		}
		url, err := storageSvc.GeneratePresignedURL(context.Background(), conversationID, rawURL, "GET", 60)
		if err != nil {
			slog.Warn("failed to generate upload file presigned URL", "path", rawURL, "err", err)
			return rawURL
		}
		return url
	}

	return rawURL
}

func pickLinkService(candidates ...storage.Service) storage.Service {
	for _, svc := range candidates {
		if svc != nil {
			return svc
		}
	}
	return nil
}

func mirrorLocalFileToOSSAndSign(
	conversationID, sourcePath, fileName string,
	localSvc storage.Service,
	ossSvc storage.Service,
) (string, error) {
	data, readErr := localSvc.ReadFile(context.Background(), conversationID, sourcePath)
	if readErr != nil {
		return "", fmt.Errorf("read local file: %w", readErr)
	}
	ossPath := buildLLMAttachmentPath(sourcePath, fileName)
	if err := ossSvc.UploadFile(context.Background(), conversationID, ossPath, data); err != nil {
		return "", fmt.Errorf("upload to oss: %w", err)
	}
	url, err := ossSvc.GeneratePresignedURL(context.Background(), conversationID, ossPath, "GET", 60)
	if err != nil {
		return "", fmt.Errorf("generate oss url: %w", err)
	}
	return url, nil
}

func uploadDataURLToStorage(conversationID, dataURL, fileName string, storageSvc storage.Service) (string, error) {
	if storageSvc == nil {
		return "", fmt.Errorf("storage service is nil")
	}
	header, payload, found := strings.Cut(dataURL, ",")
	if !found {
		return "", fmt.Errorf("invalid data URL")
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}

	if fileName == "" {
		fileName = "attachment-" + time.Now().Format("20060102150405") + detectDataURLExt(header)
	}

	path := buildLLMAttachmentPath("", fileName)
	if err := storageSvc.UploadFile(context.Background(), conversationID, path, data); err != nil {
		return "", fmt.Errorf("upload file: %w", err)
	}
	return path, nil
}

func buildLLMAttachmentPath(sourcePath, fileName string) string {
	name := strings.TrimSpace(fileName)
	if name == "" {
		name = filepath.Base(sourcePath)
	}
	name = strings.TrimSpace(filepath.Base(name))
	if name == "" || name == "." || name == "/" {
		name = "attachment.bin"
	}
	name = strings.ReplaceAll(name, " ", "_")
	return fmt.Sprintf("llm-attachments/%d_%s", time.Now().UnixMilli(), name)
}

func detectDataURLExt(header string) string {
	switch {
	case strings.Contains(header, "image/jpeg"), strings.Contains(header, "image/jpg"):
		return ".jpg"
	case strings.Contains(header, "image/gif"):
		return ".gif"
	case strings.Contains(header, "image/webp"):
		return ".webp"
	case strings.Contains(header, "application/pdf"):
		return ".pdf"
	case strings.Contains(header, "text/plain"):
		return ".txt"
	default:
		return ".png"
	}
}

// extractAllMessageTokens accounts for text, tool_use arguments, tool_result
// content, and image attachments — filling the gap that extractMessageText
// (text-only) leaves when used as a budgeting signal.
func extractAllMessageTokens(msg domain.Message) int {
	total := 0

	if len(msg.ContentParts) > 0 {
		var parts []assistantContentPart
		if err := json.Unmarshal(msg.ContentParts, &parts); err == nil {
			for _, part := range parts {
				switch part.Type {
				case "text":
					total += estimateTokens(part.Text)
				case "tool_use":
					if part.ToolCall != nil && part.ToolCall.Input != nil {
						if b, err := json.Marshal(part.ToolCall.Input); err == nil {
							total += estimateTokens(string(b))
						}
					}
				case "tool_result":
					total += estimateTokens(part.Content)
				}
			}
		}
	}

	if len(msg.Metadata) > 0 {
		var meta struct {
			Attachments []struct {
				Type string `json:"type"`
			} `json:"attachments"`
		}
		if err := json.Unmarshal(msg.Metadata, &meta); err == nil {
			for _, a := range meta.Attachments {
				if a.Type == "image" {
					total += ImageTokenEstimate
				}
			}
		}
	}

	if total <= 0 {
		total = 1
	}
	return total
}

// hardTruncateText cuts text to fit within approxTokens, appending a marker.
// Uses a rough 2-chars-per-token heuristic to balance ASCII and non-ASCII.
func hardTruncateText(text string, approxTokens int) string {
	if approxTokens <= 0 {
		return ""
	}
	maxChars := approxTokens * 2
	if len(text) <= maxChars {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	truncated := string(runes[:maxChars])
	omitted := len(runes) - maxChars
	return truncated + fmt.Sprintf("\n... [省略 %d 字符]", omitted)
}

func estimateTokens(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}

	asciiCount := 0
	nonASCII := 0
	for _, r := range text {
		if r <= 0x7F {
			asciiCount++
			continue
		}
		nonASCII++
	}

	asciiTokens := (asciiCount + 3) / 4
	total := asciiTokens + nonASCII
	if total <= 0 {
		return 1
	}
	return total
}

func estimateToolDefinitionsTokens(defs []ToolDefinition) int {
	if len(defs) == 0 {
		return 0
	}
	b, err := json.Marshal(defs)
	if err != nil {
		return ToolDefinitionsTokenEstimate
	}
	estimated := estimateTokens(string(b))
	if estimated <= 0 {
		return ToolDefinitionsTokenEstimate
	}
	return estimated
}

func hasToolDefinition(defs []ToolDefinition, toolName string) bool {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return false
	}
	for _, td := range defs {
		if strings.TrimSpace(td.Function.Name) == name {
			return true
		}
	}
	return false
}

func floatPtr(v float64) *float64 {
	return &v
}
