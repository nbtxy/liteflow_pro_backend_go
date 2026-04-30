package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/agent_profile"
	"github.com/liteflow/backend/internal/llm"
	"github.com/liteflow/backend/internal/tool"
)

const (
	MaxIterations            = 20
	ToolTimeout              = 30 * time.Second
	AnalyzeImageToolTimeout  = 120 * time.Second
	GenerateImageToolTimeout = 300 * time.Second
	LLMIterationTimeout      = 180 * time.Second
	MaxToolResult            = 200000
	MaxSameCallRepeat        = 3
)

type McpExecutorBuilder func(ctx context.Context, userID string, displayNames []string, allowedChannelNames []string) ([]tool.Tool, error)
type ExecuteOptions struct {
	MCPMode                string
	ActivatedTools         []string
	ToolPool               map[string]tool.Tool
	AllowedMcpChannelNames []string
	AgentRuntime           *agent_profile.AgentRuntime
	ConversationID         string
	UserMessageID          string
	MaxTurns               int
}

type AgentLoop struct {
	providerRouter *llm.ProviderRouter
	toolRegistry   *tool.Registry
	compactor      *llm.Compactor
	mcpExecBuilder McpExecutorBuilder
}

func NewAgentLoop(providerRouter *llm.ProviderRouter, toolRegistry *tool.Registry, compactor *llm.Compactor) *AgentLoop {
	return &AgentLoop{
		providerRouter: providerRouter,
		toolRegistry:   toolRegistry,
		compactor:      compactor,
	}
}

func (a *AgentLoop) SetMcpExecutorBuilder(fn McpExecutorBuilder) {
	a.mcpExecBuilder = fn
}

type toolCallAccumulator struct {
	id          string
	name        string
	arguments   strings.Builder
	started     bool
	emittedArgs int
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
	toolCallCounter := make(map[string]int, 16)
	for iteration := 0; iteration < maxTurns; iteration++ {
		if ctx.Err() != nil {
			return
		}

		slog.Info("agent loop iteration start", "iteration", iteration)

		llmCtx := ctx
		if opts != nil && strings.TrimSpace(opts.ConversationID) != "" && strings.TrimSpace(opts.UserMessageID) != "" {
			callCtx := llm.CallContext{
				ConversationID: strings.TrimSpace(opts.ConversationID),
				UserMessageID:  strings.TrimSpace(opts.UserMessageID),
				CallSeq:        iteration,
			}
			if llm.IsReplayMode(llm.ParseRecordMode(os.Getenv("LLM_RECORD_MODE"))) {
				if convID, msgID, ok := llm.ReplayForceIDsForMode(llm.ParseRecordMode(os.Getenv("LLM_RECORD_MODE"))); ok {
					callCtx.ConversationID = convID
					callCtx.UserMessageID = msgID
				}
			}
			llmCtx = llm.WithCallContext(ctx, callCtx)
		}

		accumulators, iterContent, err := a.streamLLMResponse(llmCtx, req, usageAcc, events, opts)
		if err != nil {
			if llm.IsContextExceeded(err) && a.compactor != nil {
				slog.Warn("context exceeded, compacting and retrying once", "iteration", iteration)
				newMsgs, cErr := a.compactor.Compact(ctx, req.Messages, req.Tools, llm.CompactOptions{Aggressive: true})
				if cErr != nil {
					events <- ErrorEvent("context_exceeded", "对话历史过长且压缩失败，请开启新会话: "+cErr.Error())
					return
				}
				req.Messages = newMsgs
				events <- SystemNoticeEvent("对话历史已自动压缩，继续响应中...")
				accumulators, iterContent, err = a.streamLLMResponse(llmCtx, req, usageAcc, events, opts)
				if err != nil {
					events <- ErrorEvent("context_exceeded", "压缩后仍超限，请开启新会话: "+err.Error())
					return
				}
			} else {
				events <- ErrorEvent("agent_loop_error", "工具调用出错: "+err.Error())
				return
			}
		}

		if len(accumulators) == 0 {
			if strings.TrimSpace(iterContent) == "" {
				slog.Warn("agent loop ended with empty response", "iteration", iteration)
				events <- ErrorEvent("empty_response", "模型未返回内容，请重试")
			}
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

		for toolSeq, tc := range toolCalls {
			repeatKey := toolCallRepeatKey(tc.name, tc.arguments)
			toolCallCounter[repeatKey]++
			if toolCallCounter[repeatKey] > MaxSameCallRepeat {
				slog.Warn("tool repeat guard triggered",
					"toolName", tc.name,
					"repeat", toolCallCounter[repeatKey],
					"maxRepeat", MaxSameCallRepeat,
				)
				events <- SystemNoticeEvent("检测到工具被重复调用，已自动停止以避免循环：" + tc.name)
				events <- ToolResultEvent(tc.id, "error", "tool re-call limit reached", nil)
				req.Messages = append(req.Messages, llm.LlmMessage{
					Role:       "tool",
					Content:    "tool re-call limit reached",
					ToolCallID: tc.id,
					Name:       tc.name,
				})
				continue
			}

			toolResultMsg, result := a.executeOneToolCallWithResult(llmCtx, tc, toolSeq, toolCtx, activatedMcpTools, opts, func(ev Event) {
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

	llmCtx, cancel := context.WithTimeout(ctx, LLMIterationTimeout)
	defer cancel()

	chunks, err := provider.StreamChat(llmCtx, req)
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
			if !acc.started && acc.id != "" && acc.name != "" {
				events <- ToolUseStartEvent(acc.id, acc.name)
				acc.started = true
			}
			if acc.started {
				args := acc.arguments.String()
				if len(args) > acc.emittedArgs {
					events <- ToolUseInputDeltaEvent(acc.id, args[acc.emittedArgs:])
					acc.emittedArgs = len(args)
				}
			}
		}

		if chunk.Usage != nil {
			usageAcc.Add(chunk.Usage)
		}
	}

	if err := llmCtx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, content.String(), fmt.Errorf("LLM 响应超时（>%s）", LLMIterationTimeout.String())
		}
		return nil, content.String(), err
	}

	return accumulators, content.String(), nil
}

type toolCallInfo struct {
	id           string
	name         string
	arguments    string
	startEmitted bool
}

func buildToolCalls(accs map[int]*toolCallAccumulator) []toolCallInfo {
	calls := make([]toolCallInfo, 0, len(accs))
	for i := 0; i < len(accs); i++ {
		acc, ok := accs[i]
		if !ok {
			continue
		}
		calls = append(calls, toolCallInfo{
			id:           acc.id,
			name:         acc.name,
			arguments:    acc.arguments.String(),
			startEmitted: acc.started,
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

func (a *AgentLoop) executeOneToolCallWithResult(ctx context.Context, tc toolCallInfo, toolSeq int,
	toolCtx *tool.ToolContext, mcpTools map[string]tool.Tool, opts *ExecuteOptions, emit func(Event)) (llm.LlmMessage, *tool.ToolResult) {

	slog.Info("tool executing",
		"toolName", tc.name,
		"callId", tc.id,
		"timeoutSec", int(toolTimeout(tc.name).Seconds()),
	)

	var input map[string]any
	var argsParseErr error
	if err := json.Unmarshal([]byte(tc.arguments), &input); err != nil {
		slog.Warn("failed to parse tool arguments", "toolName", tc.name, "err", err)
		input = map[string]any{}
		argsParseErr = err
	}

	if !tc.startEmitted {
		emit(ToolUseStartEvent(tc.id, tc.name))
	}
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
	replayed := false
	startMs := time.Now()
	if argsParseErr == nil {
		if fixture, ok, replayErr := loadToolFixture(ctx, toolSeq, tc.id, tc.name); replayErr != nil {
			slog.Error("tool replay failed", "toolName", tc.name, "err", replayErr)
			result = &tool.ToolResult{Content: "工具回放失败: " + replayErr.Error(), IsError: true}
		} else if ok && fixture != nil && fixture.Result != nil {
			replayed = true
			result = fixture.Result
		}
	}

	if result == nil {
		if t == nil {
			slog.Warn("tool not found", "toolName", tc.name)
			result = &tool.ToolResult{Content: "工具不存在: " + tc.name, IsError: true}
		} else if argsParseErr != nil {
			result = &tool.ToolResult{
				Content: fmt.Sprintf("工具参数不是合法 JSON，请重试并输出完整参数。parse error: %v", argsParseErr),
				IsError: true,
			}
		} else {
			execCtx, cancel := context.WithTimeout(ctx, toolTimeout(tc.name))
			defer cancel()
			execCtx = WithEventSink(execCtx, emit)

			// Per-call ToolContext copy so tools can access the tool_use_id of the
			// current invocation (e.g. to emit ToolProgressEvent that the frontend
			// can route to the correct tool_use card).
			tcCopy := *toolCtx
			tcCopy.ToolUseID = tc.id

			var err error
			result, err = t.Execute(execCtx, input, &tcCopy)
			if err != nil {
				slog.Error("tool execution failed", "toolName", tc.name, "err", err)
				result = &tool.ToolResult{Content: "工具执行失败: " + err.Error(), IsError: true}
			}
		}
	}

	durationMs := time.Since(startMs).Milliseconds()
	mode := toolReplayControlFromEnv()
	if shouldRecordToolFixture(mode, replayed) {
		if err := saveToolFixture(ctx, toolSeq, tc, input, result, durationMs); err != nil {
			slog.Warn("failed to save tool replay fixture", "toolName", tc.name, "err", err)
		}
	}
	status := "success"
	if result.IsError {
		status = "error"
	}
	resultContent := truncateToolResult(result.Content)

	logArgs := []any{
		"toolName", tc.name,
		"status", status,
		"durationMs", durationMs,
	}
	if result.IsError {
		logArgs = append(logArgs, "error", resultContent)
	}
	slog.Info("tool executed", logArgs...)

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

func toolCallRepeatKey(toolName, arguments string) string {
	sum := sha256.Sum256([]byte(arguments))
	return toolName + "|" + hex.EncodeToString(sum[:8])
}

func toolTimeout(toolName string) time.Duration {
	switch toolName {
	case "analyze_image":
		return AnalyzeImageToolTimeout
	case "generate_or_edit_image_nano_banana", "generate_or_edit_image_openrouter", "generate_video_seedance":
		return GenerateImageToolTimeout
	default:
		return ToolTimeout
	}
}
