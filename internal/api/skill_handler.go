package api

import (
	"net/http"

	"github.com/liteflow/backend/internal/skill"
)

type SkillHandler struct {
	skillRegistry *skill.Registry
}

func NewSkillHandler(skillRegistry *skill.Registry) *SkillHandler {
	return &SkillHandler{skillRegistry: skillRegistry}
}

func (h *SkillHandler) List(w http.ResponseWriter, r *http.Request) {
	skills := h.skillRegistry.All()
	OK(w, skills)
}

func (h *SkillHandler) Search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		BadRequest(w, "query parameter 'q' is required")
		return
	}

	result := h.skillRegistry.Search(query, nil)
	OK(w, map[string]string{"result": result})
}
