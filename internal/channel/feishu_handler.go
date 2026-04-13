package channel

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/liteflow/backend/internal/agent"
	"github.com/liteflow/backend/internal/chat"
	"github.com/liteflow/backend/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

type FeishuHandler struct {
	pool    *pgxpool.Pool
	adapter *FeishuAdapter
	chatSvc *chat.Service
	mgr     *Manager
}

func NewFeishuHandler(pool *pgxpool.Pool, adapter *FeishuAdapter, chatSvc *chat.Service) *FeishuHandler {
	return &FeishuHandler{pool: pool, adapter: adapter, chatSvc: chatSvc}
}

func (h *FeishuHandler) SetManager(mgr *Manager) {
	h.mgr = mgr
}

// HandleEvent processes a Feishu P2MessageReceiveV1 event.
func (h *FeishuHandler) HandleEvent(channel *domain.UserChannel, event *larkim.P2MessageReceiveV1) {
	data := event.Event
	if data == nil || data.Message == nil {
		return
	}

	msg := data.Message
	sender := data.Sender

	chatID := safeStr(msg.ChatId)
	msgType := safeStr(msg.MessageType)
	content := safeStr(msg.Content)
	messageID := safeStr(msg.MessageId)
	openID := ""
	if sender != nil && sender.SenderId != nil {
		openID = safeStr(sender.SenderId.OpenId)
	}

	slog.Info("feishu message received",
		"channelId", channel.ID,
		"chatId", chatID,
		"openId", openID,
		"msgType", msgType,
	)

	client := h.mgr.GetLarkClient(channel.ID)
	if client == nil {
		slog.Warn("no lark client for channel", "channelId", channel.ID)
		return
	}

	if messageID != "" {
		h.adapter.AddReaction(client, messageID)
	}

	if msgType != "text" && msgType != "post" {
		h.adapter.SendText(client, chatID, "暂时只支持文本消息哦 😊")
		return
	}

	text := h.adapter.ExtractText(content, msgType)
	if strings.TrimSpace(text) == "" {
		return
	}

	slog.Info("feishu text extracted", "channelId", channel.ID, "textLen", len(text))

	cmd := strings.TrimSpace(text)
	if cmd == "/new" || cmd == "\\new" {
		h.handleNewConversation(channel, chatID, client)
		return
	}
	if cmd == "/help" || cmd == "\\help" {
		h.handleHelp(client, chatID)
		return
	}

	h.saveSenderMapping(channel.ID, openID)

	conv, err := h.getOrCreateConversation(channel.UserID, channel.ID, chatID)
	if err != nil {
		slog.Error("create conversation failed", "err", err)
		h.adapter.SendText(client, chatID, "创建会话失败，请稍后再试")
		return
	}

	h.processChat(channel, client, chatID, conv, text)
}

func (h *FeishuHandler) processChat(channel *domain.UserChannel, client *lark.Client, chatID string, conv *domain.Conversation, text string) {
	req := chat.ChatRequest{
		ConversationID: conv.ID.String(),
		Message:        text,
	}

	ctx := context.Background()
	events := h.chatSvc.ChatStream(ctx, req, channel.UserID)

	var buffer strings.Builder
	var cardMessageID string
	var mu sync.Mutex
	lastUpdate := time.Now()

	for evt := range events {
		switch evt.Type {
		case agent.EventStreamStart:
			card := h.adapter.BuildStreamingCard("", true)
			mid, err := h.adapter.SendCardAndGetMessageID(client, chatID, card)
			if err != nil {
				slog.Warn("send streaming card failed", "err", err)
			} else {
				mu.Lock()
				cardMessageID = mid
				mu.Unlock()
			}

		case agent.EventTextDelta:
			chunk, _ := evt.Data["content"].(string)
			mu.Lock()
			buffer.WriteString(chunk)
			// Throttle card updates to ~1.2s intervals
			if cardMessageID != "" && time.Since(lastUpdate) > 1200*time.Millisecond {
				card := h.adapter.BuildStreamingCard(buffer.String(), true)
				if err := h.adapter.PatchCard(client, cardMessageID, card); err != nil {
					slog.Warn("patch card failed", "err", err)
				}
				lastUpdate = time.Now()
			}
			mu.Unlock()

		case agent.EventStreamEnd:
			mu.Lock()
			finalText := buffer.String()
			mid := cardMessageID
			mu.Unlock()

			if mid != "" {
				card := h.adapter.BuildStreamingCard(finalText, false)
				if err := h.adapter.PatchCard(client, mid, card); err != nil {
					slog.Warn("final patch card failed", "err", err)
				}
			} else {
				if finalText == "" {
					h.adapter.SendText(client, chatID, "暂未生成可用内容，请重试")
				} else if strings.Contains(finalText, "```") || strings.Contains(finalText, "**") || len(finalText) >= 500 {
					h.adapter.SendPost(client, chatID, finalText)
				} else {
					h.adapter.SendText(client, chatID, finalText)
				}
			}

		case agent.EventError:
			errMsg, _ := evt.Data["message"].(string)
			slog.Error("chat stream error", "channelId", channel.ID, "err", errMsg)
			h.adapter.SendText(client, chatID, "处理消息时出错，请稍后再试 🙏")
		}
	}
}

func (h *FeishuHandler) handleNewConversation(channel *domain.UserChannel, chatID string, client *lark.Client) {
	ctx := context.Background()
	now := time.Now()

	// Archive existing conversation
	h.pool.Exec(ctx,
		`UPDATE conversations SET archived = true, updated_at = $1 WHERE channel_id = $2 AND external_chat_id = $3 AND (archived IS NULL OR archived = false)`,
		now, channel.ID, chatID)

	// Create new
	convID := uuid.New()
	archived := false
	chType := "im"
	_, err := h.pool.Exec(ctx,
		`INSERT INTO conversations (id, user_id, channel_id, external_chat_id, channel_type, archived, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		convID, channel.UserID, channel.ID, chatID, chType, archived, now, now)
	if err != nil {
		slog.Error("create new feishu conversation failed", "err", err)
		h.adapter.SendText(client, chatID, "创建新对话失败，请稍后再试")
		return
	}

	h.adapter.SendText(client, chatID, "已开启新对话 ✨")
}

func (h *FeishuHandler) handleHelp(client *lark.Client, chatID string) {
	helpText := "🤖 LiteFlow AI 助手\n\n" +
		"直接发送消息即可开始对话\n\n" +
		"可用指令：\n" +
		"• /new — 开启新对话\n" +
		"• /help — 显示帮助信息\n\n" +
		"支持的消息类型：文本、富文本"
	h.adapter.SendText(client, chatID, helpText)
}

func (h *FeishuHandler) getOrCreateConversation(userID uuid.UUID, channelID uuid.UUID, chatID string) (*domain.Conversation, error) {
	ctx := context.Background()
	var conv domain.Conversation
	err := h.pool.QueryRow(ctx,
		`SELECT id, user_id, channel_id, external_chat_id, channel_type, created_at, updated_at
		 FROM conversations
		 WHERE channel_id = $1 AND external_chat_id = $2 AND (archived IS NULL OR archived = false)
		 ORDER BY created_at DESC LIMIT 1`,
		channelID, chatID).Scan(
		&conv.ID, &conv.UserID, &conv.ChannelID, &conv.ExternalChatID, &conv.ChannelType,
		&conv.CreatedAt, &conv.UpdatedAt,
	)
	if err == nil {
		return &conv, nil
	}

	// Create new conversation
	now := time.Now()
	conv = domain.Conversation{
		ID:             uuid.New(),
		UserID:         &userID,
		ChannelID:      &channelID,
		ExternalChatID: &chatID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	chType := "im"
	conv.ChannelType = &chType
	archived := false
	conv.Archived = &archived

	_, err = h.pool.Exec(ctx,
		`INSERT INTO conversations (id, user_id, channel_id, external_chat_id, channel_type, archived, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		conv.ID, conv.UserID, conv.ChannelID, conv.ExternalChatID, conv.ChannelType, conv.Archived, conv.CreatedAt, conv.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &conv, nil
}

func (h *FeishuHandler) saveSenderMapping(channelID uuid.UUID, openID string) {
	if openID == "" {
		return
	}
	ctx := context.Background()
	var exists bool
	h.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM channel_user_mappings WHERE channel_id = $1 AND external_user_id = $2)`,
		channelID, openID).Scan(&exists)
	if exists {
		return
	}

	h.pool.Exec(ctx,
		`INSERT INTO channel_user_mappings (id, channel_id, external_user_id, created_at) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		uuid.New(), channelID, openID, time.Now())
}

// HandleWebhook handles HTTP webhook from Feishu (challenge verification only).
func (h *FeishuHandler) HandleWebhook(w interface{ Header() interface{ Set(string, string) } }, body map[string]any) {
	// This is now a fallback - primary message handling is via WebSocket
	slog.Info("feishu webhook received", "body", body)
}

func safeStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

var _ = json.Marshal // keep import
