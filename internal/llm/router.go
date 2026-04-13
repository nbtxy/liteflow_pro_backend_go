package llm

import (
	"fmt"
	"log/slog"
)

// ProviderRouter selects the appropriate LLM provider.
type ProviderRouter struct {
	providers       map[string]Provider
	defaultProvider string
}

func NewProviderRouter(defaultProvider string) *ProviderRouter {
	return &ProviderRouter{
		providers:       make(map[string]Provider),
		defaultProvider: defaultProvider,
	}
}

func (r *ProviderRouter) Register(p Provider) {
	r.providers[p.Name()] = p
	slog.Info("registered LLM provider", "name", p.Name())
}

func (r *ProviderRouter) Default() Provider {
	p, ok := r.providers[r.defaultProvider]
	if !ok {
		// Fallback to any available provider
		for _, p := range r.providers {
			return p
		}
		return nil
	}
	return p
}

func (r *ProviderRouter) Get(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", name)
	}
	return p, nil
}

func (r *ProviderRouter) Available() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}
