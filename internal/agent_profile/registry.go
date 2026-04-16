package agent_profile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/liteflow/backend/internal/domain"
	"gopkg.in/yaml.v3"
)

type AgentRegistry struct {
	agents map[string]*domain.Agent
	main   *domain.Agent
	subs   []*domain.Agent
	mu     sync.RWMutex
}

func LoadFromDir(dir string) (*AgentRegistry, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no agent yaml files found in %s", dir)
	}

	reg := &AgentRegistry{
		agents: make(map[string]*domain.Agent, len(files)),
	}
	mainCount := 0

	for _, f := range files {
		agent, err := loadAgentFile(f)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", f, err)
		}

		expectedID := strings.TrimSuffix(filepath.Base(f), ".yaml")
		if agent.ID != expectedID {
			return nil, fmt.Errorf("%s: id %q does not match filename %q", f, agent.ID, expectedID)
		}
		if _, dup := reg.agents[agent.ID]; dup {
			return nil, fmt.Errorf("duplicate agent id: %s", agent.ID)
		}

		if agent.Type == "main" {
			mainCount++
			reg.main = agent
		} else {
			reg.subs = append(reg.subs, agent)
		}
		reg.agents[agent.ID] = agent
	}

	if mainCount != 1 {
		return nil, fmt.Errorf("expected exactly one main agent, got %d", mainCount)
	}

	sort.Slice(reg.subs, func(i, j int) bool {
		if reg.subs[i].Priority == reg.subs[j].Priority {
			return reg.subs[i].ID < reg.subs[j].ID
		}
		return reg.subs[i].Priority < reg.subs[j].Priority
	})

	return reg, nil
}

func (r *AgentRegistry) Get(id string) (*domain.Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[id]
	return a, ok
}

func (r *AgentRegistry) Main() *domain.Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.main
}

func (r *AgentRegistry) Subs() []*domain.Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*domain.Agent, len(r.subs))
	copy(out, r.subs)
	return out
}

func (r *AgentRegistry) ListAll() []*domain.Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*domain.Agent, 0, len(r.subs)+1)
	if r.main != nil {
		out = append(out, r.main)
	}
	out = append(out, r.subs...)
	return out
}

func loadAgentFile(file string) (*domain.Agent, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var a domain.Agent
	if err := yaml.Unmarshal(raw, &a); err != nil {
		return nil, err
	}

	a.ID = strings.TrimSpace(a.ID)
	a.Type = strings.ToLower(strings.TrimSpace(a.Type))
	if a.Type == "" {
		a.Type = "sub"
	}
	if a.Type != "main" && a.Type != "sub" {
		return nil, fmt.Errorf("invalid type %q", a.Type)
	}
	a.Name = strings.TrimSpace(a.Name)
	if a.ID == "" || a.Name == "" {
		return nil, fmt.Errorf("id and name are required")
	}
	a.Description = strings.TrimSpace(a.Description)
	a.AvatarURL = strings.TrimSpace(a.AvatarURL)
	a.SystemPrompt = strings.TrimSpace(a.SystemPrompt)
	a.LLM.Provider = strings.TrimSpace(a.LLM.Provider)
	a.LLM.Model = strings.TrimSpace(a.LLM.Model)
	return &a, nil
}
