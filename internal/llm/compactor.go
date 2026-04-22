package llm

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

const compactionSystemPrompt = `你正在为一段 AI 助手对话生成压缩摘要，后续会把这段摘要作为上下文继续对话。

请保留：
1. 用户的核心目标与需求
2. 已做的重要决策和结论
3. 涉及的关键文件/代码/数据（保留路径、函数名、变量名等可引用标识符）
4. 已完成的工作步骤概要
5. 尚未完成或被打断的任务
6. 遇到的错误、已知限制
7. 用户表达的偏好（语气、风格、工具选择等）

舍弃：
- 冗余的客套、确认类对话
- 已被后续信息覆盖的中间状态
- 工具执行的详细中间输出（保留关键结论即可）

严格按以下 Markdown 结构输出，不要添加其他前后缀：

## 用户意图
## 关键上下文
## 已完成工作
## 待办与未决问题
## 用户偏好`

// Compactor produces a compacted message list using an LLM summarization pass,
// mirroring Claude Code's auto-compact behavior:
//   - keep the system prefix intact
//   - summarize older history via a structured Markdown prompt
//   - preserve the last N turns verbatim
type Compactor struct {
	router   *ProviderRouter
	provider Provider
}

type CompactOptions struct {
	KeepTailTurns int
	Aggressive    bool
}

func NewCompactor(router *ProviderRouter) *Compactor {
	return &Compactor{router: router}
}

// WithProvider returns a new Compactor that uses p for summarization instead
// of the router's default. Primarily for tests.
func (c *Compactor) WithProvider(p Provider) *Compactor {
	return &Compactor{router: c.router, provider: p}
}

func (c *Compactor) summaryProvider() Provider {
	if c.provider != nil {
		return c.provider
	}
	if c.router != nil {
		return c.router.Default()
	}
	return nil
}

// Compact returns a compacted message sequence. When compaction is not
// worthwhile (history shorter than keepTurns) or fails, the original msgs
// are returned along with an error explaining why.
func (c *Compactor) Compact(ctx context.Context, msgs []LlmMessage, tools []ToolDefinition, opts CompactOptions) ([]LlmMessage, error) {
	keepTurns := opts.KeepTailTurns
	if opts.Aggressive {
		keepTurns = 1
	}
	if keepTurns < 1 {
		keepTurns = 1
	}

	systemPrefix, history := splitSystemPrefix(msgs)
	if len(history) == 0 {
		return msgs, nil
	}

	cut := findTailCutIndex(history, keepTurns)
	if cut <= 0 {
		return msgs, nil
	}

	oldHistory := sanitizeToolCallPairs(history[:cut])
	tail := history[cut:]
	if len(oldHistory) == 0 {
		return msgs, nil
	}

	provider := c.summaryProvider()
	if provider == nil {
		return msgs, fmt.Errorf("compactor: no provider available")
	}

	summaryReq := &LlmRequest{
		Messages:    buildSummaryMessages(oldHistory),
		MaxTokens:   2000,
		Temperature: floatPtr(0.0),
		Stream:      false,
	}

	resp, err := provider.Chat(ctx, summaryReq)
	if err != nil {
		return msgs, fmt.Errorf("compactor summarize: %w", err)
	}
	summaryText := ""
	if resp != nil {
		summaryText = strings.TrimSpace(resp.Content)
	}
	if summaryText == "" {
		return msgs, fmt.Errorf("compactor: empty summary")
	}

	injected := LlmMessage{
		Role:    "system",
		Content: "<compacted_history>\n" + summaryText + "\n</compacted_history>",
	}

	out := make([]LlmMessage, 0, len(systemPrefix)+1+len(tail))
	out = append(out, systemPrefix...)
	out = append(out, injected)
	out = append(out, tail...)

	slog.Info("context compacted",
		"originalMessages", len(msgs),
		"compactedMessages", len(out),
		"keptTailTurns", keepTurns,
		"oldHistorySize", len(oldHistory),
	)

	return out, nil
}

func buildSummaryMessages(oldHistory []LlmMessage) []LlmMessage {
	out := make([]LlmMessage, 0, len(oldHistory)+2)
	out = append(out, LlmMessage{Role: "system", Content: compactionSystemPrompt})
	out = append(out, oldHistory...)
	out = append(out, LlmMessage{
		Role:    "user",
		Content: "请按要求对以上对话历史输出压缩摘要。",
	})
	return out
}

func splitSystemPrefix(msgs []LlmMessage) (prefix, rest []LlmMessage) {
	i := 0
	for i < len(msgs) && msgs[i].Role == "system" {
		i++
	}
	return msgs[:i], msgs[i:]
}

// findTailCutIndex walks user messages and returns the index into history at
// which the last keepTurns conversation turns begin. Returns 0 when the
// history is short enough to keep in full (no compaction needed).
func findTailCutIndex(history []LlmMessage, keepTurns int) int {
	userIndices := make([]int, 0, 8)
	for i, m := range history {
		if m.Role == "user" {
			userIndices = append(userIndices, i)
		}
	}
	if len(userIndices) <= keepTurns {
		return 0
	}
	return userIndices[len(userIndices)-keepTurns]
}
