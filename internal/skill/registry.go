package skill

import (
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"unicode"
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
		metaName, description := extractSkillMeta(content)
		skillName := entry.Name()
		if strings.TrimSpace(metaName) != "" {
			skillName = strings.TrimSpace(metaName)
		}
		if strings.TrimSpace(description) == "" {
			description = extractFirstMeaningfulLine(content)
		}

		r.mu.Lock()
		r.skills[skillName] = &SkillInfo{
			Name:        skillName,
			Description: description,
			Content:     content,
		}
		r.mu.Unlock()

		slog.Info("loaded skill", "name", skillName, "dir", entry.Name())
	}
}

const searchTopK = 3

func (r *Registry) Search(query string, whitelist []string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.skills) == 0 {
		return "No skills available."
	}

	allowed := map[string]bool{}
	whitelistEnabled := len(whitelist) > 0
	if whitelistEnabled {
		for _, name := range whitelist {
			allowed[strings.TrimSpace(name)] = true
		}
	}

	queryTrim := strings.TrimSpace(query)
	if queryTrim == "" {
		slog.Debug("skill search: empty query", "branch", "fallback")
		return r.buildFallback(allowed, whitelistEnabled)
	}

	if hit := r.lookupByName(queryTrim, allowed, whitelistEnabled); hit != nil {
		slog.Debug("skill search: name lookup hit", "query", queryTrim, "skill", hit.Name, "branch", "lookup")
		return formatSkillContent([]*SkillInfo{hit})
	}

	queryLower := strings.ToLower(queryTrim)
	queryTokens := tokenizeQueryMixed(queryLower)

	type scored struct {
		skill *SkillInfo
		score float64
	}
	var candidates []scored
	for _, s := range r.skills {
		if whitelistEnabled && !allowed[s.Name] {
			continue
		}
		haystack := strings.ToLower(s.Name + " " + s.Description)
		skillTokens := tokenizeQueryMixed(haystack)

		hit := 0
		for _, qt := range queryTokens {
			for _, st := range skillTokens {
				if qt == st || strings.Contains(st, qt) || strings.Contains(qt, st) {
					hit++
					break
				}
			}
		}
		if hit == 0 && strings.Contains(haystack, queryLower) {
			hit = 1
		}
		if hit == 0 {
			continue
		}

		denom := len(queryTokens)
		if denom < 1 {
			denom = 1
		}
		candidates = append(candidates, scored{skill: s, score: float64(hit) / float64(denom)})
	}

	if len(candidates) == 0 {
		slog.Debug("skill search: no match", "query", queryTrim, "branch", "fallback")
		return r.buildFallback(allowed, whitelistEnabled)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].skill.Name < candidates[j].skill.Name
	})
	if len(candidates) > searchTopK {
		candidates = candidates[:searchTopK]
	}

	matched := make([]*SkillInfo, 0, len(candidates))
	names := make([]string, 0, len(candidates))
	for _, c := range candidates {
		matched = append(matched, c.skill)
		names = append(names, c.skill.Name)
	}
	slog.Debug("skill search: matched", "query", queryTrim, "count", len(matched), "skills", names, "branch", "scored")
	return formatSkillContent(matched)
}

func (r *Registry) lookupByName(query string, allowed map[string]bool, whitelistEnabled bool) *SkillInfo {
	qNorm := normalizeName(query)
	if len(qNorm) < 3 {
		return nil
	}
	var best *SkillInfo
	bestLen := 0
	for _, s := range r.skills {
		if whitelistEnabled && !allowed[s.Name] {
			continue
		}
		nNorm := normalizeName(s.Name)
		if len(nNorm) < 3 {
			continue
		}
		if qNorm == nNorm || strings.Contains(qNorm, nNorm) {
			if len(nNorm) > bestLen {
				bestLen = len(nNorm)
				best = s
			}
		}
	}
	return best
}

func (r *Registry) buildFallback(allowed map[string]bool, whitelistEnabled bool) string {
	names := make([]string, 0, len(r.skills))
	for n, s := range r.skills {
		if whitelistEnabled && !allowed[s.Name] {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString("No confident match. The following skills are available — if any fits the task, call search_skill again with its exact name as the query to load the full skill content:\n\n")
	for _, n := range names {
		s := r.skills[n]
		sb.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, s.Description))
	}
	return sb.String()
}

func formatSkillContent(skills []*SkillInfo) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d matching skill(s):\n\n", len(skills)))
	for _, s := range skills {
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

func extractSkillMeta(content string) (name string, description string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}

	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" {
			break
		}
		kv := strings.SplitN(line, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		value := strings.TrimSpace(kv[1])
		value = strings.Trim(value, `"'`)
		switch key {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	return strings.TrimSpace(name), strings.TrimSpace(description)
}

func extractFirstMeaningfulLine(content string) string {
	lines := strings.Split(content, "\n")
	inFrontMatter := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if line == "---" {
			inFrontMatter = !inFrontMatter
			continue
		}
		if inFrontMatter {
			continue
		}
		line = strings.TrimLeft(line, "#")
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// tokenizeQueryMixed splits a lowercased string into tokens suitable for
// mixed Chinese/English matching. ASCII runs produce whole words (≥2 chars).
// Chinese runs produce 2-gram tokens (pairs of adjacent Han characters),
// which are informative without the noise of single-character stop words.
func tokenizeQueryMixed(s string) []string {
	var tokens []string
	var asciiBuf strings.Builder
	var prevChinese rune

	flushAscii := func() {
		if asciiBuf.Len() == 0 {
			return
		}
		t := asciiBuf.String()
		asciiBuf.Reset()
		if utf8Len(t) >= 2 {
			tokens = append(tokens, t)
		}
	}

	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			asciiBuf.WriteRune(r)
			prevChinese = 0
		case unicode.Is(unicode.Han, r):
			flushAscii()
			if prevChinese != 0 {
				tokens = append(tokens, string(prevChinese)+string(r))
			}
			prevChinese = r
		default:
			flushAscii()
			prevChinese = 0
		}
	}
	flushAscii()
	return dedupTokens(tokens)
}

func dedupTokens(tokens []string) []string {
	seen := make(map[string]bool, len(tokens))
	out := tokens[:0]
	for _, t := range tokens {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// normalizeName strips separators (-, _, whitespace) from a skill name or
// query so that "prd-template", "PRD template", and "prdtemplate" all
// compare equal when used in lookupByName.
func normalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func utf8Len(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
