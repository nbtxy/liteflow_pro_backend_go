package agent_profile

import (
	"context"
	"fmt"
	"strings"

	"github.com/liteflow/backend/internal/domain"
	"github.com/liteflow/backend/internal/llm"
	"github.com/liteflow/backend/internal/tool"
)

type Service struct {
	registry       *AgentRegistry
	providerRouter *llm.ProviderRouter
	toolRegistry   *tool.Registry
}

func NewService(registry *AgentRegistry, providerRouter *llm.ProviderRouter, toolRegistry *tool.Registry) *Service {
	return &Service{
		registry:       registry,
		providerRouter: providerRouter,
		toolRegistry:   toolRegistry,
	}
}

func (s *Service) Resolve(ctx context.Context, agentID string) (*AgentRuntime, error) {
	rt := s.newDefaultRuntime()

	var selected *domain.Agent
	if strings.TrimSpace(agentID) != "" {
		a, ok := s.registry.Get(strings.TrimSpace(agentID))
		if !ok {
			return nil, fmt.Errorf("agent not found: %s", agentID)
		}
		selected = a
	} else {
		selected = s.registry.Main()
	}

	if selected == nil {
		return nil, fmt.Errorf("main agent not configured")
	}

	rt.AgentID = selected.ID
	rt.AgentName = selected.Name
	rt.AgentType = selected.Type
	rt.AgentAvatar = strings.TrimSpace(selected.AvatarURL)

	if selected.LLM.Provider != "" {
		if p, getErr := s.providerRouter.Get(selected.LLM.Provider); getErr == nil {
			rt.Provider = p
		}
	}
	rt.Model = strings.TrimSpace(selected.LLM.Model)
	rt.SystemPromptOverride = selected.SystemPrompt
	rt.PromptModules = cloneStrings(selected.PromptModules)
	rt.EnabledMcpChannelNames = cloneStrings(selected.EnabledMcpChannelNames)
	rt.EnabledSkills = cloneStrings(selected.EnabledSkillNames)
	rt.Temperature = selected.LLM.Temperature
	rt.MaxTokens = selected.LLM.MaxTokens

	rt.EnabledToolSet = s.buildToolPool(selected.EnabledBuiltinTools)
	rt.ToolDefs = buildToolDefs(rt.EnabledToolSet)

	return rt, nil
}

func (s *Service) GetMain(ctx context.Context) *domain.Agent {
	return s.registry.Main()
}

func (s *Service) ListAll(ctx context.Context) []*domain.Agent {
	return s.registry.ListAll()
}

func (s *Service) newDefaultRuntime() *AgentRuntime {
	allTools := make(map[string]tool.Tool)
	for _, t := range s.toolRegistry.All() {
		allTools[t.Name()] = t
	}
	return &AgentRuntime{
		AgentType:      "main",
		AgentID:        "main",
		AgentName:      "Assistant",
		Provider:       s.providerRouter.Default(),
		ToolDefs:       buildToolDefs(allTools),
		EnabledToolSet: allTools,
	}
}

func (s *Service) buildToolPool(enabledBuiltinTools []string) map[string]tool.Tool {
	if enabledBuiltinTools == nil {
		all := make(map[string]tool.Tool)
		for _, t := range s.toolRegistry.All() {
			all[t.Name()] = t
		}
		return all
	}

	result := make(map[string]tool.Tool)
	for _, name := range enabledBuiltinTools {
		if t := s.toolRegistry.Get(name); t != nil {
			result[name] = t
		}
	}
	return result
}

func buildToolDefs(pool map[string]tool.Tool) []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(pool))
	for _, t := range pool {
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

func cloneStrings(items []string) []string {
	if items == nil {
		return nil
	}
	cp := make([]string, len(items))
	copy(cp, items)
	return cp
}
