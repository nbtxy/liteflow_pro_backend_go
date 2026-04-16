package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/agent_profile"
	"github.com/liteflow/backend/internal/llm"
	"github.com/liteflow/backend/internal/tool"
)

const (
	MaxIterations = 20
	ToolTimeout   = 30 * time.Second
	MaxToolResult = 200000
)

type McpExecutorBuilder func(ctx context.Context, userID string, displayNames []string, allowedChannelNames []string) ([]tool.Tool, error)
type ExecuteOptions struct {
	MCPMode                string
	ActivatedTools         []string
	ToolPool               map[string]tool.Tool
	AllowedMcpChannelNames []string
	AgentRuntime           *agent_profile.AgentRuntime
	MaxTurns               int
}

type AgentLoop struct {
	providerRouter *llm.ProviderRouter
	toolRegistry   *tool.Registry
	mcpExecBuilder McpExecutorBuilder
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
	toolCtx *tool.ToolContext, usageAcc *llm.LlmUsage, opts *ExecuteOptions) <-chan Event {

	events := make(chan Event, 64)
	go func() {
		defer close(events)
		a.run(ctx, req, toolCtx, usageAcc, events, opts)
	}()
	return events
}

func (a *AgentLoop) run(ctx context.Context, req *llm.LlmRequest,
	toolCtx *tool.ToolContext, usageAcc *llm.LlmUsage, events chan<- Event, opts *ExecuteOptions) {

	normalTools := req.Tools
	allowedMcpChannels := []string(nil)
	if opts != nil {
		allowedMcpChannels = opts.AllowedMcpChannelNames
	}
	activatedMcpTools := make(map[string]tool.Tool)
	mcpModeActive := false
	if opts != nil && strings.EqualFold(opts.MCPMode, "active") && len(opts.ActivatedTools) > 0 {
		if a.mcpExecBuilder != nil && toolCtx.UserID != uuid.Nil {
			executors, err := a.mcpExecBuilder(ctx, toolCtx.UserID.String(), opts.ActivatedTools, allowedMcpChannels)
			if err != nil {
				slog.Warn("failed to restore MCP executors", "err", err)
			} else {
				for _, exec := range executors {
					activatedMcpTools[exec.Name()] = exec
				}
			}
		}
		if len(activatedMcpTools) > 0 {
			inactiveTool := tool.NewInactiveMCP()
			activatedMcpTools[inactiveTool.Name()] = inactiveTool
			req.Tools = a.buildMcpActiveToolDefs(normalTools, activatedMcpTools)
			mcpModeActive = true
			slog.Info("restored MCP active mode", "mcpToolCount", len(activatedMcpTools))
		}
	}

	maxTurns := MaxIterations
	if opts != nil && opts.MaxTurns > 0 && opts.MaxTurns < MaxIterations {
		maxTurns = opts.MaxTurns
	}
	for iteration := 0; iteration < maxTurns; iteration++ {
		if ctx.Err() != nil {
			return
		}

		slog.Info("agent loop iteration start", "iteration", iteration)

		accumulators, iterContent, err := a.streamLLMResponse(ctx, req, usageAcc, events, opts)
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
			toolResultMsg, result := a.executeOneToolCallWithResult(ctx, tc, toolCtx, activatedMcpTools, opts, func(ev Event) {
				events <- ev
			})
			req.Messages = append(req.Messages, toolResultMsg)

			if result != nil && result.Metadata != nil {
				if mode, ok := result.Metadata["mcp_mode"].(string); ok {
					switch mode {
					case "active":
						nextMcpMode = true
					case "inactive":
						nextMcpMode = false
					}
				}
				if activated, ok := result.Metadata["activated_tools"]; ok {
					newlyActivated = append(newlyActivated, parseStringSlice(activated)...)
				}
			}
		}

		if nextMcpMode && len(newlyActivated) > 0 && a.mcpExecBuilder != nil && toolCtx.UserID != uuid.Nil {
			executors, err := a.mcpExecBuilder(ctx, toolCtx.UserID.String(), newlyActivated, allowedMcpChannels)
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

	slog.Warn("agent loop reached max iterations", "max", maxTurns)
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
	usageAcc *llm.LlmUsage, events chan<- Event, opts *ExecuteOptions) (map[int]*toolCallAccumulator, string, error) {

	provider := a.providerRouter.Default()
	if opts != nil && opts.AgentRuntime != nil && opts.AgentRuntime.Provider != nil {
		provider = opts.AgentRuntime.Provider
	}
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
	toolCtx *tool.ToolContext, mcpTools map[string]tool.Tool, opts *ExecuteOptions, emit func(Event)) (llm.LlmMessage, *tool.ToolResult) {

	slog.Info("tool executing", "toolName", tc.name, "callId", tc.id)

	var input map[string]any
	var argsParseErr error
	if err := json.Unmarshal([]byte(tc.arguments), &input); err != nil {
		slog.Warn("failed to parse tool arguments", "toolName", tc.name, "err", err)
		input = map[string]any{}
		argsParseErr = err
	}

	emit(ToolUseStartEvent(tc.id, tc.name))
	emit(ToolUseInputEvent(tc.id, input))

	if tc.name == "search_skill" && opts != nil && opts.AgentRuntime != nil && opts.AgentRuntime.EnabledSkills != nil {
		if _, exists := input["whitelist"]; !exists {
			input["whitelist"] = opts.AgentRuntime.EnabledSkills
		}
	}

	t := tool.Tool(nil)
	if opts != nil && opts.ToolPool != nil {
		t = opts.ToolPool[tc.name]
	}
	if t == nil {
		t = a.toolRegistry.Get(tc.name)
	}
	if t == nil {
		t = mcpTools[tc.name]
	}

	var result *tool.ToolResult
	startMs := time.Now()

	if t == nil {
		slog.Warn("tool not found", "toolName", tc.name)
		result = &tool.ToolResult{Content: "工具不存在: " + tc.name, IsError: true}
	} else if argsParseErr != nil {
		result = &tool.ToolResult{
			Content: fmt.Sprintf("工具参数不是合法 JSON，请重试并输出完整参数。parse error: %v", argsParseErr),
			IsError: true,
		}
	} else {
		execCtx, cancel := context.WithTimeout(ctx, ToolTimeout)
		defer cancel()
		execCtx = WithEventSink(execCtx, emit)

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
	resultContent := truncateToolResult(result.Content)

	slog.Info("tool executed",
		"toolName", tc.name,
		"status", status,
		"durationMs", durationMs,
	)

	emit(ToolResultEvent(tc.id, status, resultContent, result.Metadata))

	if result.Metadata != nil {
		if artifactEvents := buildArtifactEvents(result.Metadata, tc.name, input); len(artifactEvents) > 0 {
			for _, ev := range artifactEvents {
				emit(ev)
			}
		}
	}

	toolResultMsg := llm.LlmMessage{
		Role:       "tool",
		Content:    resultContent,
		ToolCallID: tc.id,
		Name:       tc.name,
	}

	return toolResultMsg, result
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
	} else if toolName == "create_file" || toolName == "str_replace" {
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

func parseStringSlice(v any) []string {
	if names, ok := v.([]string); ok {
		return names
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok && s != "" {
			result = append(result, s)
		}
	}
	return result
}
