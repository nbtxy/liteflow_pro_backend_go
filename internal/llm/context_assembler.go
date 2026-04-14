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
)

type ContextAssembler struct {
	templateEngine *PromptTemplateEngine
	providerRouter *ProviderRouter
	storageSvc     storage.Service
	ossLinkSvc     storage.Service
	toolDefs       []ToolDefinition
	hasTools       bool
}

func NewContextAssembler(
	templateEngine *PromptTemplateEngine,
	providerRouter *ProviderRouter,
	storageSvc storage.Service,
	ossLinkSvc storage.Service,
	toolDefs []ToolDefinition,
) *ContextAssembler {
	return &ContextAssembler{
		templateEngine: templateEngine,
		providerRouter: providerRouter,
		storageSvc:     storageSvc,
		ossLinkSvc:     ossLinkSvc,
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
	visionSupported := false
	if provider != nil {
		maxContext = provider.MaxContextTokens()
		visionSupported = provider.SupportsVision()
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

			llmHistory = append([]LlmMessage{
				buildLlmMessage(msg, conversationID, visionSupported, ca.storageSvc, ca.ossLinkSvc),
			}, llmHistory...)
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

func buildLlmMessage(
	msg domain.Message,
	conversationID string,
	visionSupported bool,
	storageSvc storage.Service,
	ossLinkSvc storage.Service,
) LlmMessage {
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
				Name     string `json:"name"`
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
			if len(meta.Attachments) > 0 {
				textParts := make([]string, 0, len(meta.Attachments))
				images := make([]ImageContent, 0, len(meta.Attachments))

				for _, a := range meta.Attachments {
					fileName := a.FileName
					if fileName == "" {
						fileName = a.Name
					}
					publicURL := resolveAttachmentURL(conversationID, a.URL, fileName, storageSvc, ossLinkSvc)
					if publicURL == "" {
						continue
					}

					if a.Type == "image" && visionSupported {
						images = append(images, ImageContent{
							Type:     "image_url",
							ImageURL: &ImageURL{URL: publicURL},
						})
						continue
					}

					label := "附件"
					if a.Type == "image" {
						label = "图片附件"
					}
					if fileName == "" {
						fileName = "file"
					}
					textParts = append(textParts, fmt.Sprintf("[%s: %s](%s)", label, fileName, publicURL))
				}

				combinedContent := content
				if len(textParts) > 0 {
					if combinedContent != "" {
						combinedContent += "\n"
					}
					combinedContent += strings.Join(textParts, "\n")
				}

				if len(images) > 0 || combinedContent != msg.Content {
					return LlmMessage{Role: "user", Content: combinedContent, Images: images}
				}
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

func estimateTokens(text string) int {
	return len(text)
}

func floatPtr(v float64) *float64 {
	return &v
}
