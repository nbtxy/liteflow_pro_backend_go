package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeProvider is a stub Provider used to capture summarization requests.
type fakeProvider struct {
	name           string
	lastReq        *LlmRequest
	cannedResponse string
	cannedErr      error
	maxContext     int
	callCount      int
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) MaxContextTokens() int {
	if f.maxContext == 0 {
		return 128000
	}
	return f.maxContext
}
func (f *fakeProvider) SupportsVision() bool { return false }
func (f *fakeProvider) StreamChat(ctx context.Context, req *LlmRequest) (<-chan LlmChunk, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeProvider) Chat(ctx context.Context, req *LlmRequest) (*LlmResponse, error) {
	f.callCount++
	f.lastReq = req
	if f.cannedErr != nil {
		return nil, f.cannedErr
	}
	return &LlmResponse{Content: f.cannedResponse}, nil
}

func TestCompactor_ShortHistoryReturnsUnchanged(t *testing.T) {
	fp := &fakeProvider{cannedResponse: "summary"}
	c := (&Compactor{}).WithProvider(fp)

	msgs := []LlmMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	out, err := c.Compact(context.Background(), msgs, nil, CompactOptions{KeepTailTurns: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != len(msgs) {
		t.Errorf("expected unchanged, got %d vs %d", len(out), len(msgs))
	}
	if fp.callCount != 0 {
		t.Errorf("provider should not be called for short history, got %d calls", fp.callCount)
	}
}

func TestCompactor_KeepsSystemPrefixAndTail(t *testing.T) {
	fp := &fakeProvider{cannedResponse: "## 用户意图\nfoo"}
	c := (&Compactor{}).WithProvider(fp)

	// 5 user turns; keepTailTurns=2 should keep tail starting at user #4.
	msgs := []LlmMessage{
		{Role: "system", Content: "sys1"},
		{Role: "system", Content: "sys2"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
		{Role: "user", Content: "u4"},
		{Role: "assistant", Content: "a4"},
		{Role: "user", Content: "u5"},
		{Role: "assistant", Content: "a5"},
	}
	out, err := c.Compact(context.Background(), msgs, nil, CompactOptions{KeepTailTurns: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// expect: [sys1, sys2, system(summary), u4, a4, u5, a5]
	if len(out) != 7 {
		t.Fatalf("expected 7 messages, got %d: %+v", len(out), out)
	}
	if out[0].Role != "system" || out[0].Content != "sys1" {
		t.Errorf("out[0] mismatch: %+v", out[0])
	}
	if out[1].Role != "system" || out[1].Content != "sys2" {
		t.Errorf("out[1] mismatch: %+v", out[1])
	}
	if out[2].Role != "system" || !strings.Contains(out[2].Content, "<compacted_history>") || !strings.Contains(out[2].Content, "foo") {
		t.Errorf("out[2] should wrap summary in compacted_history tag: %+v", out[2])
	}
	if out[3].Content != "u4" || out[5].Content != "u5" {
		t.Errorf("tail user messages wrong: out[3]=%q out[5]=%q", out[3].Content, out[5].Content)
	}
}

func TestCompactor_AggressiveForcesKeepOne(t *testing.T) {
	fp := &fakeProvider{cannedResponse: "summary text"}
	c := (&Compactor{}).WithProvider(fp)
	msgs := []LlmMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
	}
	out, err := c.Compact(context.Background(), msgs, nil, CompactOptions{KeepTailTurns: 5, Aggressive: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Aggressive overrides KeepTailTurns → 1; tail = [u3, a3], plus injected system summary → 3.
	if len(out) != 3 {
		t.Fatalf("expected 3 (summary + u3 + a3), got %d: %+v", len(out), out)
	}
	if out[0].Role != "system" || !strings.Contains(out[0].Content, "summary text") {
		t.Errorf("out[0] should be summary system msg: %+v", out[0])
	}
	if out[1].Content != "u3" || out[2].Content != "a3" {
		t.Errorf("tail mismatch: %+v", out[1:])
	}
}

func TestCompactor_PreservesToolCallPairsInOldHistory(t *testing.T) {
	fp := &fakeProvider{cannedResponse: "summary"}
	c := (&Compactor{}).WithProvider(fp)

	// Arrange: older history contains a tool-call assistant + tool reply that
	// must both be kept intact in the summary input, or both dropped. Tail
	// starts at u3.
	msgs := []LlmMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: ToolCallFunc{Name: "search", Arguments: `{"q":"x"}`},
		}}},
		{Role: "tool", ToolCallID: "call_1", Content: "result"},
		{Role: "assistant", Content: "a1-final"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
	}
	_, err := c.Compact(context.Background(), msgs, nil, CompactOptions{KeepTailTurns: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp.lastReq == nil {
		t.Fatalf("provider not called")
	}
	// Inspect summarization request: find the assistant tool_call and its
	// matching tool result in the old history slice the compactor sent.
	var sawToolCall, sawToolResult bool
	for _, m := range fp.lastReq.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 && m.ToolCalls[0].ID == "call_1" {
			sawToolCall = true
		}
		if m.Role == "tool" && m.ToolCallID == "call_1" {
			sawToolResult = true
		}
	}
	if sawToolCall != sawToolResult {
		t.Errorf("tool_call/tool_result pair must be kept together: call=%v result=%v", sawToolCall, sawToolResult)
	}
}

func TestCompactor_ProviderErrorReturnsOriginal(t *testing.T) {
	fp := &fakeProvider{cannedErr: errors.New("network down")}
	c := (&Compactor{}).WithProvider(fp)

	msgs := []LlmMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
	}
	out, err := c.Compact(context.Background(), msgs, nil, CompactOptions{KeepTailTurns: 1})
	if err == nil {
		t.Fatalf("expected error from provider failure")
	}
	if len(out) != len(msgs) {
		t.Errorf("on error, original msgs should be returned")
	}
}

func TestCompactor_EmptySummaryReturnsError(t *testing.T) {
	fp := &fakeProvider{cannedResponse: "   "}
	c := (&Compactor{}).WithProvider(fp)
	msgs := []LlmMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
	}
	_, err := c.Compact(context.Background(), msgs, nil, CompactOptions{KeepTailTurns: 1})
	if err == nil {
		t.Fatalf("expected error on empty summary")
	}
}

func TestCompactor_NoProviderReturnsError(t *testing.T) {
	c := &Compactor{}
	msgs := []LlmMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
	}
	_, err := c.Compact(context.Background(), msgs, nil, CompactOptions{KeepTailTurns: 1})
	if err == nil {
		t.Fatalf("expected error when no provider available")
	}
}

func TestFindTailCutIndex(t *testing.T) {
	history := []LlmMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
	}
	if got := findTailCutIndex(history, 3); got != 0 {
		t.Errorf("keepTurns=3 with 3 users: want 0 (no cut), got %d", got)
	}
	if got := findTailCutIndex(history, 2); got != 2 {
		t.Errorf("keepTurns=2: want 2 (cut before u2), got %d", got)
	}
	if got := findTailCutIndex(history, 1); got != 4 {
		t.Errorf("keepTurns=1: want 4 (cut before u3), got %d", got)
	}
}

func TestSplitSystemPrefix(t *testing.T) {
	msgs := []LlmMessage{
		{Role: "system", Content: "s1"},
		{Role: "system", Content: "s2"},
		{Role: "user", Content: "u"},
		{Role: "assistant", Content: "a"},
	}
	prefix, rest := splitSystemPrefix(msgs)
	if len(prefix) != 2 || len(rest) != 2 {
		t.Fatalf("split wrong: prefix=%d rest=%d", len(prefix), len(rest))
	}
	if prefix[0].Content != "s1" || rest[0].Content != "u" {
		t.Errorf("split content wrong")
	}
}
