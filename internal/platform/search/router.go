package search

import (
	"fmt"
	"log/slog"
)

type ProviderRouter struct {
	providers       map[string]Provider
	defaultProvider string
}

func NewProviderRouter(defaultName string) *ProviderRouter {
	return &ProviderRouter{
		providers:       make(map[string]Provider),
		defaultProvider: defaultName,
	}
}

func (r *ProviderRouter) Register(p Provider) {
	r.providers[p.Name()] = p
	slog.Info("registered search provider", "name", p.Name())
}

func (r *ProviderRouter) Default() Provider {
	if p, ok := r.providers[r.defaultProvider]; ok {
		return p
	}
	for _, p := range r.providers {
		return p
	}
	return nil
}

func (r *ProviderRouter) Get(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("search provider not found: %s", name)
	}
	return p, nil
}
