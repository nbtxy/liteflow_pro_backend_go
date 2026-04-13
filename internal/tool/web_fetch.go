package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type WebFetchTool struct{}

func NewWebFetch() *WebFetchTool {
	return &WebFetchTool{}
}

func (t *WebFetchTool) Name() string        { return "web_fetch" }
func (t *WebFetchTool) Description() string  { return "Fetch content from a URL" }
func (t *WebFetchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "URL to fetch",
			},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, input map[string]any, _ *ToolContext) (*ToolResult, error) {
	url, _ := input["url"].(string)
	if url == "" {
		return &ToolResult{Content: "url is required", IsError: true}, nil
	}

	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}

	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("invalid URL: %v", err), IsError: true}, nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; LiteFlow/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("fetch error: %v", err), IsError: true}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024)) // 100KB limit
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}

	content := string(body)
	if len(content) > 3000 {
		content = content[:3000] + "\n...(truncated)"
	}

	return &ToolResult{
		Content: fmt.Sprintf("URL: %s\nStatus: %d\n\n%s", url, resp.StatusCode, content),
	}, nil
}
