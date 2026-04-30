package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/liteflow/backend/internal/llm"
	"github.com/liteflow/backend/internal/tool"
)

func TestToolReplay_SaveAndLoadFixture(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LLM_SAVE_REQUEST_DIR", dir)
	t.Setenv("LLM_RECORD_MODE", "record")

	ctx := llm.WithCallContext(context.Background(), llm.CallContext{
		ConversationID: "conv_1",
		UserMessageID:  "msg_1",
		CallSeq:        0,
	})
	tc := toolCallInfo{id: "call_1", name: "search_web"}
	input := map[string]any{"query": "liteflow"}
	result := &tool.ToolResult{Content: "ok", IsError: false}

	if err := saveToolFixture(ctx, 0, tc, input, result, 123); err != nil {
		t.Fatalf("save fixture failed: %v", err)
	}
	path := filepath.Join(dir, "conv_1", "msg_1", "00_tool_00_search_web.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture not found: %v", err)
	}

	t.Setenv("LLM_RECORD_MODE", "replay")
	fixture, ok, err := loadToolFixture(ctx, 0, "call_1", "search_web")
	if err != nil {
		t.Fatalf("load fixture failed: %v", err)
	}
	if !ok || fixture == nil || fixture.Result == nil {
		t.Fatalf("expected fixture loaded")
	}
	if fixture.Result.Content != "ok" {
		t.Fatalf("unexpected result content: %s", fixture.Result.Content)
	}
}

func TestToolReplay_ReplayOnlyMissReturnsError(t *testing.T) {
	t.Setenv("LLM_SAVE_REQUEST_DIR", t.TempDir())
	t.Setenv("LLM_RECORD_MODE", "replay")

	ctx := llm.WithCallContext(context.Background(), llm.CallContext{
		ConversationID: "conv_x",
		UserMessageID:  "msg_x",
		CallSeq:        0,
	})
	_, ok, err := loadToolFixture(ctx, 0, "call_x", "search_web")
	if err == nil || ok {
		t.Fatalf("expected replay-only miss error, got ok=%v err=%v", ok, err)
	}
}

func TestToolReplay_RecordModeMissNoError(t *testing.T) {
	t.Setenv("LLM_SAVE_REQUEST_DIR", t.TempDir())
	t.Setenv("LLM_RECORD_MODE", "record")

	ctx := llm.WithCallContext(context.Background(), llm.CallContext{
		ConversationID: "conv_y",
		UserMessageID:  "msg_y",
		CallSeq:        1,
	})
	_, ok, err := loadToolFixture(ctx, 0, "call_y", "search_web")
	if err != nil || ok {
		t.Fatalf("expected record mode to bypass replay lookup, got ok=%v err=%v", ok, err)
	}
}
