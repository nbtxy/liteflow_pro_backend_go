package memory

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/llm"
)

type Extractor struct {
	providerRouter *llm.ProviderRouter
	memorySvc      *Service
}

func NewExtractor(providerRouter *llm.ProviderRouter, memorySvc *Service) *Extractor {
	return &Extractor{
		providerRouter: providerRouter,
		memorySvc:      memorySvc,
	}
}

const extractionPrompt = `分析以下对话，提取值得记住的用户信息。返回JSON数组，每个元素包含:
- category: IDENTITY/PREFERENCE/PROJECT/SKILL/CONTEXT/PERSONAL/INSTRUCTION
- content: 简洁的记忆内容（一句话）

只提取明确的、有价值的信息。如果没有值得记住的内容，返回空数组 []。

对话内容:
%s`

func (e *Extractor) ExtractFromConversation(ctx context.Context, userID uuid.UUID,
	conversationID uuid.UUID, messages string) error {

	provider := e.providerRouter.Default()
	if provider == nil {
		return nil
	}

	prompt := strings.ReplaceAll(extractionPrompt, "%s", messages)
	temp := 0.3
	req := &llm.LlmRequest{
		Messages:    []llm.LlmMessage{{Role: "user", Content: prompt}},
		MaxTokens:   500,
		Temperature: &temp,
	}

	resp, err := provider.Chat(ctx, req)
	if err != nil {
		return err
	}

	var memories []struct {
		Category string `json:"category"`
		Content  string `json:"content"`
	}

	content := strings.TrimSpace(resp.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	if err := json.Unmarshal([]byte(content), &memories); err != nil {
		slog.Debug("failed to parse memory extraction", "err", err, "content", content)
		return nil
	}

	for _, m := range memories {
		if m.Category == "" || m.Content == "" {
			continue
		}
		if err := e.memorySvc.Add(ctx, userID, m.Category, m.Content, &conversationID); err != nil {
			slog.Error("failed to save extracted memory", "err", err)
		}
	}

	slog.Info("extracted memories", "count", len(memories), "conversationId", conversationID)
	return nil
}
