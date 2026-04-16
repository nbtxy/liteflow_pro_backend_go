package skill

import (
	"fmt"
	"strings"
	"testing"
)

func newTestRegistry(skills map[string]*SkillInfo) *Registry {
	r := &Registry{skills: make(map[string]*SkillInfo, len(skills))}
	for name, s := range skills {
		if s.Name == "" {
			s.Name = name
		}
		r.skills[s.Name] = s
	}
	return r
}

func TestSearch_NameLookupExact(t *testing.T) {
	r := newTestRegistry(map[string]*SkillInfo{
		"prd-template": {
			Description: "Write product requirement documents.",
			Content:     "PRD_BODY_MARKER",
		},
		"code-review-checklist": {
			Description: "Review code for quality.",
			Content:     "CR_BODY_MARKER",
		},
	})
	out := r.Search("prd-template", nil)
	if !strings.Contains(out, "PRD_BODY_MARKER") {
		t.Fatalf("expected prd content, got: %s", out)
	}
	if strings.Contains(out, "CR_BODY_MARKER") {
		t.Fatalf("unexpected other skill content: %s", out)
	}
}

func TestSearch_NameLookupNormalized(t *testing.T) {
	r := newTestRegistry(map[string]*SkillInfo{
		"prd-template": {Description: "desc", Content: "PRD_BODY_MARKER"},
	})
	for _, q := range []string{"PRD template", "prdtemplate", "PRD-TEMPLATE", "  prd-template  "} {
		out := r.Search(q, nil)
		if !strings.Contains(out, "PRD_BODY_MARKER") {
			t.Fatalf("query %q: expected PRD_BODY_MARKER, got: %s", q, out)
		}
	}
}

func TestSearch_NameLookupEmbeddedInPhrase(t *testing.T) {
	r := newTestRegistry(map[string]*SkillInfo{
		"code-review-checklist": {Description: "desc", Content: "CR_BODY_MARKER"},
		"prd-template":          {Description: "desc", Content: "PRD_BODY_MARKER"},
	})
	out := r.Search("please use code-review-checklist now", nil)
	if !strings.Contains(out, "CR_BODY_MARKER") {
		t.Fatalf("expected CR_BODY_MARKER, got: %s", out)
	}
	if strings.Contains(out, "PRD_BODY_MARKER") {
		t.Fatalf("unexpected PRD match: %s", out)
	}
}

func TestSearch_ChineseQuery(t *testing.T) {
	r := newTestRegistry(map[string]*SkillInfo{
		"prd-template": {
			Description: "Use this skill whenever the user asks to draft a PRD. 帮我写份需求文档.",
			Content:     "PRD_BODY_MARKER",
		},
		"code-review-checklist": {
			Description: "Review code for quality, bugs, security.",
			Content:     "CR_BODY_MARKER",
		},
	})
	out := r.Search("帮我写需求文档", nil)
	if !strings.Contains(out, "PRD_BODY_MARKER") {
		t.Fatalf("expected PRD_BODY_MARKER for Chinese query, got: %s", out)
	}
	if strings.Contains(out, "CR_BODY_MARKER") {
		t.Fatalf("CR should not match Chinese query: %s", out)
	}
}

func TestSearch_EnglishKeyword(t *testing.T) {
	r := newTestRegistry(map[string]*SkillInfo{
		"code-review-checklist": {
			Description: "Review code for quality, bugs, security, performance.",
			Content:     "CR_BODY_MARKER",
		},
		"prd-template": {
			Description: "Write product requirement documents.",
			Content:     "PRD_BODY_MARKER",
		},
	})
	out := r.Search("code review", nil)
	if !strings.Contains(out, "CR_BODY_MARKER") {
		t.Fatalf("expected CR_BODY_MARKER, got: %s", out)
	}
}

func TestSearch_FallbackGuidance(t *testing.T) {
	r := newTestRegistry(map[string]*SkillInfo{
		"prd-template":          {Description: "DESC_PRD", Content: "PRD_BODY_MARKER"},
		"code-review-checklist": {Description: "DESC_CR", Content: "CR_BODY_MARKER"},
	})
	out := r.Search("totallyunrelatedquery", nil)
	if !strings.Contains(out, "call search_skill again") {
		t.Fatalf("expected fallback guidance copy, got: %s", out)
	}
	if !strings.Contains(out, "prd-template") || !strings.Contains(out, "code-review-checklist") {
		t.Fatalf("fallback should list all skill names: %s", out)
	}
	if !strings.Contains(out, "DESC_PRD") || !strings.Contains(out, "DESC_CR") {
		t.Fatalf("fallback should show descriptions: %s", out)
	}
	if strings.Contains(out, "PRD_BODY_MARKER") || strings.Contains(out, "CR_BODY_MARKER") {
		t.Fatalf("fallback should not dump full content: %s", out)
	}
}

func TestSearch_WhitelistScopes(t *testing.T) {
	r := newTestRegistry(map[string]*SkillInfo{
		"prd-template":          {Description: "DESC_PRD", Content: "PRD_BODY_MARKER"},
		"code-review-checklist": {Description: "DESC_CR", Content: "CR_BODY_MARKER"},
	})
	out := r.Search("prd-template", []string{"code-review-checklist"})
	if strings.Contains(out, "PRD_BODY_MARKER") {
		t.Fatalf("whitelist must block prd-template content: %s", out)
	}
	if strings.Contains(out, "prd-template") {
		t.Fatalf("whitelist fallback must not mention non-allowed skill: %s", out)
	}
	if !strings.Contains(out, "code-review-checklist") {
		t.Fatalf("whitelist fallback should mention allowed skill: %s", out)
	}
}

func TestSearch_TopKLimit(t *testing.T) {
	skills := make(map[string]*SkillInfo)
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("helper-%d", i)
		skills[name] = &SkillInfo{
			Description: "covers reviewing code quality for this skill",
			Content:     fmt.Sprintf("BODY-%d", i),
		}
	}
	r := newTestRegistry(skills)
	out := r.Search("review quality", nil)
	count := strings.Count(out, "BODY-")
	if count != searchTopK {
		t.Fatalf("expected Top-%d results, got %d: %s", searchTopK, count, out)
	}
}

func TestTokenizeQueryMixed(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"hello world", []string{"hello", "world"}},
		{"code-review", []string{"code", "review"}},
		{"帮我写prd", []string{"帮我", "我写", "prd"}},
		{"a", nil},
		{"ab", []string{"ab"}},
		{"", nil},
	}
	for _, tt := range tests {
		got := tokenizeQueryMixed(tt.in)
		if !equalStringSlice(got, tt.want) {
			t.Errorf("tokenizeQueryMixed(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"prd-template", "prdtemplate"},
		{"PRD Template", "prdtemplate"},
		{"prd_template ", "prdtemplate"},
		{"code-review-checklist", "codereviewchecklist"},
		{"  ", ""},
	}
	for _, tt := range tests {
		if got := normalizeName(tt.in); got != tt.want {
			t.Errorf("normalizeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
