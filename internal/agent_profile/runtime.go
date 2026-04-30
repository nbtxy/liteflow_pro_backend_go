package agent_profile

import (
	"github.com/liteflow/backend/internal/llm"
	"github.com/liteflow/backend/internal/tool"
)

type AgentRuntime struct {
	AgentID                string
	AgentName              string
	AgentAvatar            string
	AgentType              string
	Provider               llm.Provider
	Model                  string
	ToolDefs               []llm.ToolDefinition
	EnabledToolSet         map[string]tool.Tool
	EnabledMcpChannelNames []string
	EnabledSkills          []string // nil means all
	PromptModules          []string // nil means default
	SystemPromptOverride   string
	Temperature            *float64
	MaxTokens              int
}
