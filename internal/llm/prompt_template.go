package llm

import (
	"embed"
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

//go:embed prompts/*.md
var promptsFS embed.FS

var varPattern = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

type PromptTemplateEngine struct {
	mu        sync.RWMutex
	templates map[string]string
}

func NewPromptTemplateEngine() *PromptTemplateEngine {
	e := &PromptTemplateEngine{
		templates: make(map[string]string),
	}
	e.loadTemplates()
	return e
}

func (e *PromptTemplateEngine) loadTemplates() {
	e.loadDir("prompts", "")
}

func (e *PromptTemplateEngine) loadDir(fsDir, prefix string) {
	entries, err := promptsFS.ReadDir(fsDir)
	if err != nil {
		slog.Warn("failed to read prompts dir", "dir", fsDir, "err", err)
		return
	}

	for _, entry := range entries {
		fullPath := fsDir + "/" + entry.Name()
		key := prefix + entry.Name()

		if entry.IsDir() {
			e.loadDir(fullPath, prefix+entry.Name()+"/")
			continue
		}

		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		data, err := promptsFS.ReadFile(fullPath)
		if err != nil {
			slog.Error("failed to read prompt file", "path", fullPath, "err", err)
			continue
		}

		templateKey := strings.TrimSuffix(key, ".md")
		e.mu.Lock()
		e.templates[templateKey] = string(data)
		e.mu.Unlock()
		slog.Info("loaded prompt template", "key", templateKey)
	}
}

func (e *PromptTemplateEngine) Assemble(modules []string, variables map[string]string) string {
	var sb strings.Builder

	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, module := range modules {
		template, ok := e.templates[module]
		if !ok {
			slog.Warn("prompt module not found", "module", module)
			continue
		}

		processed := e.processVariables(template, variables)
		sb.WriteString(processed)
		sb.WriteString("\n\n")
	}

	return strings.TrimSpace(sb.String())
}

func (e *PromptTemplateEngine) processVariables(template string, variables map[string]string) string {
	if len(variables) == 0 {
		return template
	}

	return varPattern.ReplaceAllStringFunc(template, func(match string) string {
		key := match[1 : len(match)-1]
		if val, ok := variables[key]; ok {
			return val
		}
		return match
	})
}

func (e *PromptTemplateEngine) Reload() {
	e.mu.Lock()
	e.templates = make(map[string]string)
	e.mu.Unlock()
	e.loadTemplates()
}
