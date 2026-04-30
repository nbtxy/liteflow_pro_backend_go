package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liteflow/backend/internal/llm"
	"github.com/liteflow/backend/internal/tool"
)

const toolReplaySchemaV1 = "tool_v1"

type toolReplayFixture struct {
	Schema         string           `json:"schema"`
	SavedAt        string           `json:"saved_at"`
	ConversationID string           `json:"conversation_id,omitempty"`
	UserMessageID  string           `json:"user_message_id,omitempty"`
	CallSeq        int              `json:"call_seq,omitempty"`
	ToolSeq        int              `json:"tool_seq,omitempty"`
	ToolCallID     string           `json:"tool_call_id,omitempty"`
	ToolName       string           `json:"tool_name"`
	Input          map[string]any   `json:"input,omitempty"`
	Result         *tool.ToolResult `json:"result,omitempty"`
	DurationMS     int64            `json:"duration_ms,omitempty"`
	Status         string           `json:"status,omitempty"`
	Error          *string          `json:"error,omitempty"`
}

func toolReplayControlFromEnv() llm.Mode {
	mode := llm.ParseRecordMode(os.Getenv("LLM_RECORD_MODE"))
	return mode
}

func shouldRecordToolFixture(mode llm.Mode, replayed bool) bool {
	switch mode {
	case llm.RecordOnly:
		return true
	default:
		return false
	}
}

func loadToolFixture(ctx context.Context, toolSeq int, toolCallID, toolName string) (*toolReplayFixture, bool, error) {
	mode := toolReplayControlFromEnv()
	if !llm.IsReplayMode(mode) {
		return nil, false, nil
	}

	path, err := toolFixturePathForLookup(ctx, toolSeq, toolName)
	if err != nil {
		if mode == llm.ReplayOnly {
			return nil, false, err
		}
		return nil, false, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if mode == llm.ReplayOnly {
				return nil, false, fmt.Errorf("tool replay fixture not found: %s", path)
			}
		}
		return nil, false, err
	}

	var fixture toolReplayFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return nil, false, err
	}
	if fixture.ToolCallID != "" && toolCallID != "" && fixture.ToolCallID != toolCallID {
		if mode == llm.ReplayOnly {
			return nil, false, fmt.Errorf("tool replay fixture call id mismatch: expect %s got %s", toolCallID, fixture.ToolCallID)
		}
		return nil, false, nil
	}
	if fixture.Result == nil {
		return nil, false, fmt.Errorf("tool replay fixture has empty result: %s", path)
	}
	return &fixture, true, nil
}

func saveToolFixture(ctx context.Context, toolSeq int, tc toolCallInfo, input map[string]any, result *tool.ToolResult, durationMS int64) error {
	path, err := toolFixturePathForSave(ctx, toolSeq, tc.name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	cc, _ := llm.CallContextFromCtx(ctx)
	status := "success"
	if result != nil && result.IsError {
		status = "error"
	}

	fixture := toolReplayFixture{
		Schema:         toolReplaySchemaV1,
		SavedAt:        time.Now().Format(time.RFC3339Nano),
		ConversationID: cc.ConversationID,
		UserMessageID:  cc.UserMessageID,
		CallSeq:        cc.CallSeq,
		ToolSeq:        toolSeq,
		ToolCallID:     tc.id,
		ToolName:       tc.name,
		Input:          input,
		Result:         result,
		DurationMS:     durationMS,
		Status:         status,
	}

	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func toolFixturePathForLookup(ctx context.Context, toolSeq int, toolName string) (string, error) {
	root := llm.DefaultFixtureDir()
	cc, ok := llm.CallContextFromCtx(ctx)
	if ok && strings.TrimSpace(cc.ConversationID) != "" && strings.TrimSpace(cc.UserMessageID) != "" {
		return filepath.Join(
			root,
			cc.ConversationID,
			cc.UserMessageID,
			fmt.Sprintf("%02d_tool_%02d_%s.json", cc.CallSeq, toolSeq, sanitizeToolReplayName(toolName)),
		), nil
	}
	return "", errors.New("tool replay context missing conversation/message/call_seq")
}

func toolFixturePathForSave(ctx context.Context, toolSeq int, toolName string) (string, error) {
	root := llm.DefaultFixtureDir()
	cc, ok := llm.CallContextFromCtx(ctx)
	if ok && strings.TrimSpace(cc.ConversationID) != "" && strings.TrimSpace(cc.UserMessageID) != "" {
		return filepath.Join(
			root,
			cc.ConversationID,
			cc.UserMessageID,
			fmt.Sprintf("%02d_tool_%02d_%s.json", cc.CallSeq, toolSeq, sanitizeToolReplayName(toolName)),
		), nil
	}
	return filepath.Join(
		root,
		"_orphan",
		fmt.Sprintf("tool_%s_%d.json", sanitizeToolReplayName(toolName), time.Now().UnixNano()),
	), nil
}

func sanitizeToolReplayName(name string) string {
	if name == "" {
		return "unknown"
	}
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, " ", "_")
	return name
}
