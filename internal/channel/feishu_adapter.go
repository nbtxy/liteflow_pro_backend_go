package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/google/uuid"
)

var (
	atMentionRe    = regexp.MustCompile(`@_user_\d+\s*`)
	boldRe         = regexp.MustCompile(`\*\*(.*?)\*\*`)
	linkRe         = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

type FeishuAdapter struct{}

func NewFeishuAdapter() *FeishuAdapter {
	return &FeishuAdapter{}
}

// ExtractText extracts plain text from Feishu message content.
func (a *FeishuAdapter) ExtractText(content, msgType string) string {
	if content == "" {
		return ""
	}

	switch msgType {
	case "text":
		var parsed map[string]any
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			return content
		}
		text, _ := parsed["text"].(string)
		return strings.TrimSpace(atMentionRe.ReplaceAllString(text, ""))

	case "post":
		return a.extractPostText(content)
	}

	return content
}

func (a *FeishuAdapter) extractPostText(content string) string {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return content
	}

	for _, lang := range []string{"zh_cn", "en_us"} {
		langObj, ok := parsed[lang].(map[string]any)
		if !ok {
			continue
		}
		lines, ok := langObj["content"].([]any)
		if !ok {
			continue
		}

		var sb strings.Builder
		for _, line := range lines {
			elems, ok := line.([]any)
			if !ok {
				continue
			}
			for _, elem := range elems {
				m, ok := elem.(map[string]any)
				if !ok {
					continue
				}
				tag, _ := m["tag"].(string)
				if tag == "text" || tag == "a" {
					if t, ok := m["text"].(string); ok {
						sb.WriteString(t)
					}
				}
			}
			sb.WriteString("\n")
		}
		return strings.TrimSpace(sb.String())
	}
	return ""
}

// BuildTextMessage builds a Feishu text message JSON.
func (a *FeishuAdapter) BuildTextMessage(text string) string {
	if len([]rune(text)) > 4000 {
		text = string([]rune(text)[:3990]) + "\n...(内容已截断)"
	}
	b, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return `{"text":"消息格式化失败"}`
	}
	return string(b)
}

// MarkdownToFeishuPost converts Markdown to Feishu post format.
func (a *FeishuAdapter) MarkdownToFeishuPost(markdown string) string {
	if markdown == "" {
		return a.BuildTextMessage("(空回复)")
	}
	if len(markdown) < 500 && !strings.Contains(markdown, "```") && !strings.Contains(markdown, "**") {
		return a.BuildTextMessage(markdown)
	}

	var content [][]map[string]any
	lines := strings.Split(markdown, "\n")
	inCodeBlock := false
	var codeBuffer strings.Builder
	codeLang := ""

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			if inCodeBlock {
				content = append(content, []map[string]any{{
					"tag":      "code_block",
					"language": codeLang,
					"text":     codeBuffer.String(),
				}})
				codeBuffer.Reset()
				inCodeBlock = false
			} else {
				inCodeBlock = true
				codeLang = strings.TrimSpace(strings.TrimPrefix(line, "```"))
				if codeLang == "" {
					codeLang = "plain"
				}
			}
		} else if inCodeBlock {
			if codeBuffer.Len() > 0 {
				codeBuffer.WriteString("\n")
			}
			codeBuffer.WriteString(line)
		} else {
			content = append(content, a.buildPostLine(line))
		}
	}

	if inCodeBlock && codeBuffer.Len() > 0 {
		content = append(content, []map[string]any{{
			"tag":      "code_block",
			"language": codeLang,
			"text":     codeBuffer.String(),
		}})
	}

	post := map[string]any{
		"zh_cn": map[string]any{
			"title":   "",
			"content": content,
		},
	}

	b, err := json.Marshal(post)
	if err != nil {
		return a.BuildTextMessage(markdown)
	}
	return string(b)
}

func (a *FeishuAdapter) buildPostLine(line string) []map[string]any {
	var elements []map[string]any

	matches := linkRe.FindAllStringSubmatchIndex(line, -1)
	lastEnd := 0
	for _, loc := range matches {
		if loc[0] > lastEnd {
			a.addTextNodes(&elements, line[lastEnd:loc[0]])
		}
		linkText := line[loc[2]:loc[3]]
		linkHref := line[loc[4]:loc[5]]
		elements = append(elements, map[string]any{
			"tag":  "a",
			"text": linkText,
			"href": linkHref,
		})
		lastEnd = loc[1]
	}
	if lastEnd < len(line) {
		a.addTextNodes(&elements, line[lastEnd:])
	}

	if len(elements) == 0 {
		elements = append(elements, map[string]any{"tag": "text", "text": "\n"})
	}
	return elements
}

func (a *FeishuAdapter) addTextNodes(elements *[]map[string]any, text string) {
	matches := boldRe.FindAllStringSubmatchIndex(text, -1)
	lastEnd := 0
	for _, loc := range matches {
		if loc[0] > lastEnd {
			*elements = append(*elements, map[string]any{"tag": "text", "text": text[lastEnd:loc[0]]})
		}
		*elements = append(*elements, map[string]any{"tag": "text", "text": text[loc[2]:loc[3]], "bold": true})
		lastEnd = loc[1]
	}
	if lastEnd < len(text) {
		*elements = append(*elements, map[string]any{"tag": "text", "text": text[lastEnd:]})
	}
}

// BuildStreamingCard builds an interactive card for streaming replies.
func (a *FeishuAdapter) BuildStreamingCard(text string, inProgress bool) string {
	safe := strings.ReplaceAll(text, "\\", "\\\\")
	safe = strings.ReplaceAll(safe, "\"", "\\\"")
	safe = strings.ReplaceAll(safe, "\n", "\\n")
	safe = strings.ReplaceAll(safe, "\r", "\\r")
	safe = strings.ReplaceAll(safe, "\t", "\\t")

	title := "AI 已生成"
	template := "green"
	if inProgress {
		title = "AI 正在生成…"
		template = "blue"
	}

	return fmt.Sprintf(
		`{"config":{"wide_screen_mode":true,"update_multi":true},"header":{"template":"%s","title":{"tag":"plain_text","content":"%s"}},"elements":[{"tag":"markdown","content":"%s"}]}`,
		template, title, safe)
}

// SendText sends a text message to a Feishu chat.
func (a *FeishuAdapter) SendText(client *lark.Client, chatID, text string) error {
	content := a.BuildTextMessage(text)
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType("text").
			Content(content).
			Build()).
		Build()

	resp, err := client.Im.V1.Message.Create(context.Background(), req, larkcore.WithTenantKey(""))
	if err != nil {
		return fmt.Errorf("send text: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("send text failed: code=%d, msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// SendPost sends a post (rich text) message to a Feishu chat.
func (a *FeishuAdapter) SendPost(client *lark.Client, chatID, markdown string) error {
	content := a.MarkdownToFeishuPost(markdown)
	msgType := "post"
	if !strings.HasPrefix(content, `{"zh_cn"`) {
		msgType = "text"
	}

	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType(msgType).
			Content(content).
			Build()).
		Build()

	resp, err := client.Im.V1.Message.Create(context.Background(), req, larkcore.WithTenantKey(""))
	if err != nil {
		return fmt.Errorf("send post: %w", err)
	}
	if !resp.Success() {
		slog.Warn("send post failed, falling back to text", "code", resp.Code, "msg", resp.Msg)
		return a.SendText(client, chatID, markdown)
	}
	return nil
}

// SendCardAndGetMessageID sends an interactive card and returns the message ID.
func (a *FeishuAdapter) SendCardAndGetMessageID(client *lark.Client, chatID, cardJSON string) (string, error) {
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType("interactive").
			Content(cardJSON).
			Build()).
		Build()

	resp, err := client.Im.V1.Message.Create(context.Background(), req, larkcore.WithTenantKey(""))
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", fmt.Errorf("send card: code=%d, msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data != nil && resp.Data.MessageId != nil {
		return *resp.Data.MessageId, nil
	}
	return "", nil
}

// PatchCard updates an existing interactive card message.
func (a *FeishuAdapter) PatchCard(client *lark.Client, messageID, cardJSON string) error {
	req := larkim.NewPatchMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().
			Content(cardJSON).
			Build()).
		Build()

	resp, err := client.Im.V1.Message.Patch(context.Background(), req, larkcore.WithTenantKey(""))
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("patch card: code=%d, msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// AddReaction adds a reaction to a message.
func (a *FeishuAdapter) AddReaction(client *lark.Client, messageID string) {
	req := larkim.NewCreateMessageReactionReqBuilder().
		MessageId(messageID).
		Body(larkim.NewCreateMessageReactionReqBodyBuilder().
			ReactionType(larkim.NewEmojiBuilder().EmojiType("Get").Build()).
			Build()).
		Build()

	resp, err := client.Im.V1.MessageReaction.Create(context.Background(), req, larkcore.WithTenantKey(""))
	if err != nil {
		slog.Warn("add reaction failed", "err", err)
		return
	}
	if !resp.Success() {
		slog.Warn("add reaction failed", "code", resp.Code, "msg", resp.Msg)
	}
}

var _ = uuid.Nil // keep import
