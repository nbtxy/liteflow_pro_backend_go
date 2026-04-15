package agent

type EventType string

const (
	EventStreamStart    EventType = "stream_start"
	EventStreamEnd      EventType = "stream_end"
	EventTextDelta      EventType = "text_delta"
	EventToolUseStart   EventType = "tool_use_start"
	EventToolUseInput   EventType = "tool_use_input"
	EventToolResult     EventType = "tool_result"
	EventArtifactCreate EventType = "artifact_created"
	EventArtifactUpdate EventType = "artifact_updated"
	EventError          EventType = "error"
)

type Event struct {
	Type EventType      `json:"type"`
	Data map[string]any `json:"data,omitempty"`
}

func NewEvent(typ EventType, data map[string]any) Event {
	if data == nil {
		data = make(map[string]any)
	}
	data["type"] = string(typ)
	return Event{Type: typ, Data: data}
}

func TextDeltaEvent(content string) Event {
	return NewEvent(EventTextDelta, map[string]any{"content": content})
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

func ToolResultEvent(toolUseID, status, content string) Event {
	return NewEvent(EventToolResult, map[string]any{
		"toolUseId": toolUseID,
		"status":    status,
		"content":   content,
	})
}

func ErrorEvent(code, message string) Event {
	return NewEvent(EventError, map[string]any{
		"code":    code,
		"message": message,
	})
}
