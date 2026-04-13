package skill

import (
	"embed"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

//go:embed skills/**/*.md
var skillsFS embed.FS

type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content,omitempty"`
}

type Registry struct {
	mu     sync.RWMutex
	skills map[string]*SkillInfo
}

func NewRegistry() *Registry {
	r := &Registry{
		skills: make(map[string]*SkillInfo),
	}
	r.loadSkills()
	return r
}

func (r *Registry) loadSkills() {
	entries, err := skillsFS.ReadDir("skills")
	if err != nil {
		slog.Debug("no skills directory found", "err", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillFile := fmt.Sprintf("skills/%s/SKILL.md", entry.Name())
		data, err := skillsFS.ReadFile(skillFile)
		if err != nil {
			continue
		}

		content := string(data)
		description := extractFirstLine(content)

		r.mu.Lock()
		r.skills[entry.Name()] = &SkillInfo{
			Name:        entry.Name(),
			Description: description,
			Content:     content,
		}
		r.mu.Unlock()

		slog.Info("loaded skill", "name", entry.Name())
	}
}

func (r *Registry) Search(query string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.skills) == 0 {
		return "No skills available."
	}

	queryLower := strings.ToLower(query)
	var matched []*SkillInfo
	for _, s := range r.skills {
		if strings.Contains(strings.ToLower(s.Name), queryLower) ||
			strings.Contains(strings.ToLower(s.Description), queryLower) {
			matched = append(matched, s)
		}
	}

	if len(matched) == 0 {
		var sb strings.Builder
		sb.WriteString("No matching skills found. Available skills:\n")
		for _, s := range r.skills {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, s.Description))
		}
		return sb.String()
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d matching skill(s):\n\n", len(matched)))
	for _, s := range matched {
		sb.WriteString(fmt.Sprintf("### %s\n%s\n\n", s.Name, s.Content))
	}
	return sb.String()
}

func (r *Registry) All() []*SkillInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var skills []*SkillInfo
	for _, s := range r.skills {
		skills = append(skills, &SkillInfo{Name: s.Name, Description: s.Description})
	}
	return skills
}

func extractFirstLine(content string) string {
	lines := strings.SplitN(content, "\n", 2)
	line := strings.TrimSpace(lines[0])
	line = strings.TrimPrefix(line, "#")
	return strings.TrimSpace(line)
}
