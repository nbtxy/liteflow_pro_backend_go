package api

import (
	"net/http"

	"github.com/liteflow/backend/internal/agent_profile"
	"github.com/liteflow/backend/internal/auth"
	"github.com/liteflow/backend/internal/domain"
)

type AgentHandler struct {
	agentSvc *agent_profile.Service
}

func NewAgentHandler(agentSvc *agent_profile.Service) *AgentHandler {
	return &AgentHandler{agentSvc: agentSvc}
}

func (h *AgentHandler) List(w http.ResponseWriter, r *http.Request) {
	if _, err := auth.GetUserID(r.Context()); err != nil {
		Unauthorized(w, "unauthorized")
		return
	}
	agents := h.agentSvc.ListAll(r.Context())
	if agents == nil {
		OK(w, []domain.Agent{})
		return
	}
	items := make([]domain.Agent, 0, len(agents))
	for _, a := range agents {
		if a != nil {
			items = append(items, *a)
		}
	}
	OK(w, items)
}

func (h *AgentHandler) GetMain(w http.ResponseWriter, r *http.Request) {
	if _, err := auth.GetUserID(r.Context()); err != nil {
		Unauthorized(w, "unauthorized")
		return
	}
	agent := h.agentSvc.GetMain(r.Context())
	if agent == nil {
		NotFound(w, "main agent not found")
		return
	}
	OK(w, agent)
}
