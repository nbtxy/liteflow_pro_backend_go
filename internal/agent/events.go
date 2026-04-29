package agent

import "github.com/liteflow/backend/internal/platform/events"

type EventType = events.EventType

type Event = events.Event

const (
	EventStreamStart       EventType = "stream_start"
	EventStreamEnd         EventType = "stream_end"
	EventTextDelta         EventType = "text_delta"
	EventToolUseStart      EventType = "tool_use_start"
	EventToolUseInput      EventType = "tool_use_input"
	EventToolUseInputDelta EventType = "tool_use_input_delta"
	EventToolResult        EventType = "tool_result"
	EventDelegationStart   EventType = "delegation_start"
	EventDelegationDelta   EventType = "delegation_delta"
	EventDelegationEnd     EventType = "delegation_end"
	EventArtifactCreate    EventType = "artifact_created"
	EventArtifactUpdate    EventType = "artifact_updated"
	EventError             EventType = "error"
	EventSystemNotice      EventType = "system_notice"
	EventToolProgress      EventType = "tool_progress"
)

func NewEvent(typ EventType, data map[string]any) Event {
	return events.NewEvent(typ, data)
}

// TextDeltaEvent emits an LLM streaming token. It must NOT be used for tool
// progress narration — tools should use ToolProgressEvent instead, otherwise
// narration text gets concatenated into the assistant message body on the
// frontend and may accumulate visually if the agent loop re-executes the tool.
func TextDeltaEvent(content string) Event {
	return NewEvent(EventTextDelta, map[string]any{"content": content})
}

// ToolProgressEvent emits a non-token-stream progress update from inside a
// running tool (e.g. "preparing references", "calling model"). The frontend
// renders it on the corresponding tool_use card, not in the assistant text.
// toolUseID may be empty if the tool cannot determine it; in that case the
// frontend will fall back to matching by toolName.
func ToolProgressEvent(toolUseID, toolName, content string) Event {
	return NewEvent(EventToolProgress, map[string]any{
		"toolUseId": toolUseID,
		"toolName":  toolName,
		"content":   content,
	})
}

func ToolUseStartEvent(toolUseID, toolName string) Event {
	return NewEvent(EventToolUseStart, map[string]any{
		"toolUseId": toolUseID,
		"toolName":  toolName,
	})
}

func ToolUseInputEvent(toolUseID string, input map[string]any) Event {
	return NewEvent(EventToolUseInput, map[string]any{
		"toolUseId": toolUseID,
		"input":     input,
	})
}

func ToolUseInputDeltaEvent(toolUseID, inputDelta string) Event {
	return NewEvent(EventToolUseInputDelta, map[string]any{
		"toolUseId":   toolUseID,
		"input_delta": inputDelta,
	})
}

func ToolResultEvent(toolUseID, status, content string, metadata map[string]any) Event {
	data := map[string]any{
		"toolUseId": toolUseID,
		"status":    status,
		"content":   content,
	}
	if metadata != nil {
		if mode, ok := metadata["mcp_mode"]; ok {
			data["mcp_mode"] = mode
		}
		if activated, ok := metadata["activated_tools"]; ok {
			data["activated_tools"] = activated
		}
		if sourceMessageID, ok := metadata["source_message_id"]; ok {
			data["source_message_id"] = sourceMessageID
		}
	}
	return NewEvent(EventToolResult, data)
}

func ErrorEvent(code, message string) Event {
	return NewEvent(EventError, map[string]any{
		"code":    code,
		"message": message,
	})
}

// SystemNoticeEvent carries a non-fatal informational message (e.g. auto
// context compaction notice). Frontend may render it as a subtle banner or
// ignore it; it must not interrupt the ongoing response.
func SystemNoticeEvent(message string) Event {
	return NewEvent(EventSystemNotice, map[string]any{
		"message": message,
	})
}
