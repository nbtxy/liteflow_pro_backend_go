package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/llm"
	"github.com/liteflow/backend/internal/tool"
)

const (
	MaxIterations = 20
	ToolTimeout   = 30 * time.Second
	MaxToolResult = 2000
)

type McpExecutorBuilder func(ctx context.Context, userID string, displayNames []string) ([]tool.Tool, error)

type AgentLoop struct {
	providerRouter  *llm.ProviderRouter
	toolRegistry    *tool.Registry
	mcpExecBuilder  McpExecutorBuilder
}

func NewAgentLoop(providerRouter *llm.ProviderRouter, toolRegistry *tool.Registry) *AgentLoop {
	return &AgentLoop{
		providerRouter: providerRouter,
		toolRegistry:   toolRegistry,
	}
}

func (a *AgentLoop) SetMcpExecutorBuilder(fn McpExecutorBuilder) {
	a.mcpExecBuilder = fn
}

type toolCallAccumulator struct {
	id        string
	name      string
	arguments strings.Builder
}

func (a *AgentLoop) Execute(ctx context.Context, req *llm.LlmRequest,
	toolCtx *tool.ToolContext, usageAcc *llm.LlmUsage) <-chan Event {

	events := make(chan Event, 64)
	go func() {
		defer close(events)
		a.run(ctx, req, toolCtx, usageAcc, events)
	}()
	return events
}

func (a *AgentLoop) run(ctx context.Context, req *llm.LlmRequest,
	toolCtx *tool.ToolContext, usageAcc *llm.LlmUsage, events chan<- Event) {

	normalTools := req.Tools
	activatedMcpTools := make(map[string]tool.Tool)
	mcpModeActive := false

	for iteration := 0; iteration < MaxIterations; iteration++ {
		if ctx.Err() != nil {
			return
		}

		slog.Info("agent loop iteration start", "iteration", iteration)

		accumulators, iterContent, err := a.streamLLMResponse(ctx, req, usageAcc, events)
		if err != nil {
			events <- ErrorEvent("agent_loop_error", "工具调用出错: "+err.Error())
			return
		}

		if len(accumulators) == 0 {
			return
		}

		toolCalls := buildToolCalls(accumulators)
		slog.Info("LLM requested tool calls",
			"toolCallCount", len(toolCalls),
			"iteration", iteration,
		)

		assistantMsg := llm.LlmMessage{
			Role:      "assistant",
			Content:   iterContent,
			ToolCalls: toToolCallSlice(toolCalls),
		}

		req.Messages = append(req.Messages, assistantMsg)

		var newlyActivated []string
		nextMcpMode := mcpModeActive

		for _, tc := range toolCalls {
			toolEvents, toolResultMsg, result := a.executeOneToolCallWithResult(ctx, tc, toolCtx, activatedMcpTools)
			for _, ev := range toolEvents {
				events <- ev
			}
			req.Messages = append(req.Messages, toolResultMsg)

			if result != nil && result.Metadata != nil {
				if mode, ok := result.Metadata["mcp_mode"].(string); ok {
					if mode == "active" {
						nextMcpMode = true
					} else if mode == "inactive" {
						nextMcpMode = false
					}
				}
				if activated, ok := result.Metadata["activated_tools"]; ok {
					if names, ok := activated.([]string); ok {
						newlyActivated = append(newlyActivated, names...)
					}
				}
			}
		}

		if nextMcpMode && len(newlyActivated) > 0 && a.mcpExecBuilder != nil && toolCtx.UserID != uuid.Nil {
			executors, err := a.mcpExecBuilder(ctx, toolCtx.UserID.String(), newlyActivated)
			if err != nil {
				slog.Warn("failed to build MCP executors", "err", err)
			} else {
				for _, exec := range executors {
					activatedMcpTools[exec.Name()] = exec
				}
			}
		}

		if nextMcpMode != mcpModeActive || (nextMcpMode && len(newlyActivated) > 0) {
			mcpModeActive = nextMcpMode
			if mcpModeActive {
				inactiveTool := tool.NewInactiveMCP()
				activatedMcpTools[inactiveTool.Name()] = inactiveTool
				req.Tools = a.buildMcpActiveToolDefs(normalTools, activatedMcpTools)
				slog.Info("MCP mode active, tools swapped", "mcpToolCount", len(activatedMcpTools))
			} else {
				for k := range activatedMcpTools {
					delete(activatedMcpTools, k)
				}
				req.Tools = normalTools
				slog.Info("MCP mode deactivated, tools restored")
			}
		}
	}

	slog.Warn("agent loop reached max iterations", "max", MaxIterations)
	events <- TextDeltaEvent("已达到最大工具调用次数，以下是基于已有信息的回答。")
}

func (a *AgentLoop) buildMcpActiveToolDefs(_ []llm.ToolDefinition, mcpTools map[string]tool.Tool) []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(mcpTools))
	for _, t := range mcpTools {
		defs = append(defs, llm.ToolDefinition{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.InputSchema(),
			},
		})
	}
	return defs
}

func (a *AgentLoop) streamLLMResponse(ctx context.Context, req *llm.LlmRequest,
	usageAcc *llm.LlmUsage, events chan<- Event) (map[int]*toolCallAccumulator, string, error) {

	provider := a.providerRouter.Default()
	if provider == nil {
		return nil, "", nil
	}

	chunks, err := provider.StreamChat(ctx, req)
	if err != nil {
		return nil, "", err
	}

	accumulators := make(map[int]*toolCallAccumulator)
	var content strings.Builder

	for chunk := range chunks {
		if chunk.Err != nil {
			return nil, content.String(), chunk.Err
		}

		if chunk.Content != "" {
			content.WriteString(chunk.Content)
			events <- TextDeltaEvent(chunk.Content)
		}

		if chunk.HasToolCallDelta() {
			idx := chunk.ToolCallIndex
			acc, ok := accumulators[idx]
			if !ok {
				acc = &toolCallAccumulator{}
				accumulators[idx] = acc
			}
			if chunk.ToolCallID != "" {
				acc.id = chunk.ToolCallID
			}
			if chunk.ToolCallFunctionName != "" {
				acc.name = chunk.ToolCallFunctionName
			}
			if chunk.ToolCallFunctionArgs != "" {
				acc.arguments.WriteString(chunk.ToolCallFunctionArgs)
			}
		}

		if chunk.Usage != nil {
			usageAcc.Add(chunk.Usage)
		}
	}

	return accumulators, content.String(), nil
}

type toolCallInfo struct {
	id        string
	name      string
	arguments string
}

func buildToolCalls(accs map[int]*toolCallAccumulator) []toolCallInfo {
	calls := make([]toolCallInfo, 0, len(accs))
	for i := 0; i < len(accs); i++ {
		acc, ok := accs[i]
		if !ok {
			continue
		}
		calls = append(calls, toolCallInfo{
			id:        acc.id,
			name:      acc.name,
			arguments: acc.arguments.String(),
		})
	}
	return calls
}

func toToolCallSlice(calls []toolCallInfo) []llm.ToolCall {
	result := make([]llm.ToolCall, len(calls))
	for i, c := range calls {
		result[i] = llm.ToolCall{
			ID:   c.id,
			Type: "function",
			Function: llm.ToolCallFunc{
				Name:      c.name,
				Arguments: c.arguments,
			},
		}
	}
	return result
}

func (a *AgentLoop) executeOneToolCallWithResult(ctx context.Context, tc toolCallInfo,
	toolCtx *tool.ToolContext, mcpTools map[string]tool.Tool) ([]Event, llm.LlmMessage, *tool.ToolResult) {

	var events []Event

	slog.Info("tool executing", "toolName", tc.name, "callId", tc.id)

	var input map[string]any
	if err := json.Unmarshal([]byte(tc.arguments), &input); err != nil {
		slog.Warn("failed to parse tool arguments", "toolName", tc.name, "err", err)
		input = map[string]any{}
	}

	events = append(events, ToolUseStartEvent(tc.id, tc.name))
	events = append(events, ToolUseInputEvent(tc.id, input))

	t := a.toolRegistry.Get(tc.name)
	if t == nil {
		t = mcpTools[tc.name]
	}

	var result *tool.ToolResult
	startMs := time.Now()

	if t == nil {
		slog.Warn("tool not found", "toolName", tc.name)
		result = &tool.ToolResult{Content: "工具不存在: " + tc.name, IsError: true}
	} else {
		execCtx, cancel := context.WithTimeout(ctx, ToolTimeout)
		defer cancel()

		var err error
		result, err = t.Execute(execCtx, input, toolCtx)
		if err != nil {
			slog.Error("tool execution failed", "toolName", tc.name, "err", err)
			result = &tool.ToolResult{Content: "工具执行失败: " + err.Error(), IsError: true}
		}
	}

	durationMs := time.Since(startMs).Milliseconds()
	status := "success"
	if result.IsError {
		status = "error"
	}

	slog.Info("tool executed",
		"toolName", tc.name,
		"status", status,
		"durationMs", durationMs,
	)

	events = append(events, ToolResultEvent(tc.id, status))

	if result.Metadata != nil {
		if artifactEvents := buildArtifactEvents(result.Metadata, tc.name, input); len(artifactEvents) > 0 {
			events = append(events, artifactEvents...)
		}
	}

	toolResultMsg := llm.LlmMessage{
		Role:       "tool",
		Content:    truncateToolResult(result.Content),
		ToolCallID: tc.id,
		Name:       tc.name,
	}

	return events, toolResultMsg, result
}

func buildArtifactEvents(metadata map[string]any, toolName string, input map[string]any) []Event {
	var events []Event

	artifactID, ok := metadata["artifact_id"]
	if !ok {
		if artifacts, ok := metadata["artifacts"]; ok {
			if list, ok := artifacts.([]any); ok {
				for _, item := range list {
					if m, ok := item.(map[string]any); ok {
						if evts := buildSingleArtifactEvent(m, toolName, input); len(evts) > 0 {
							events = append(events, evts...)
						}
					}
				}
			}
		}
		return events
	}

	_ = artifactID
	return buildSingleArtifactEvent(metadata, toolName, input)
}

func buildSingleArtifactEvent(meta map[string]any, toolName string, input map[string]any) []Event {
	artifactID, ok := meta["artifact_id"]
	if !ok {
		return nil
	}

	version := 1
	if v, ok := meta["version"]; ok {
		if n, ok := v.(float64); ok {
			version = int(n)
		}
	}

	eventType := EventArtifactCreate
	if version > 1 {
		eventType = EventArtifactUpdate
	}

	title := ""
	if t, ok := meta["title"].(string); ok {
		title = t
	} else if toolName == "create_file" || toolName == "edit_file" {
		if p, ok := input["path"].(string); ok {
			title = p
		}
	}

	data := map[string]any{
		"type":         string(eventType),
		"artifactId":   artifactID,
		"artifactType": meta["type"],
		"title":        title,
		"version":      version,
	}

	if fs, ok := meta["file_size"]; ok {
		data["fileSize"] = fs
	}
	if version > 1 {
		if pid, ok := meta["parent_id"]; ok {
			data["parentId"] = pid
		}
	}

	return []Event{{Type: eventType, Data: data}}
}

func truncateToolResult(content string) string {
	if len(content) > MaxToolResult {
		return content[:MaxToolResult] + "\n... [结果已截断]"
	}
	return content
}
