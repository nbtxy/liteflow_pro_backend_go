package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type HttpRequestTool struct{}

func NewHttpRequest() *HttpRequestTool {
	return &HttpRequestTool{}
}

func (t *HttpRequestTool) Name() string { return "http_request" }
func (t *HttpRequestTool) Description() string {
	return "发送 HTTP 请求，支持 GET/POST/PUT/PATCH/DELETE 方法，可自定义 headers 和 body"
}
func (t *HttpRequestTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"method": map[string]any{
				"type":        "string",
				"description": "HTTP 方法",
				"enum":        []string{"GET", "POST", "PUT", "PATCH", "DELETE"},
			},
			"url": map[string]any{
				"type":        "string",
				"description": "请求 URL",
			},
			"headers": map[string]any{
				"type":        "object",
				"description": "请求头，键值对形式，如 {\"Content-Type\": \"application/json\", \"Authorization\": \"Bearer xxx\"}",
			},
			"body": map[string]any{
				"type":        "string",
				"description": "请求体内容（POST/PUT/PATCH 时使用）",
			},
		},
		"required": []string{"method", "url"},
	}
}

func (t *HttpRequestTool) Execute(ctx context.Context, input map[string]any, _ *ToolContext) (*ToolResult, error) {
	method, _ := input["method"].(string)
	url, _ := input["url"].(string)
	body, _ := input["body"].(string)

	if method == "" {
		return &ToolResult{Content: "method is required", IsError: true}, nil
	}
	if url == "" {
		return &ToolResult{Content: "url is required", IsError: true}, nil
	}

	method = strings.ToUpper(method)
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return &ToolResult{Content: "不支持的 HTTP 方法: " + method, IsError: true}, nil
	}

	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("创建请求失败: %v", err), IsError: true}, nil
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; LiteFlow/1.0)")

	if headers, ok := input["headers"].(map[string]any); ok {
		for k, v := range headers {
			if s, ok := v.(string); ok {
				req.Header.Set(k, s)
			}
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("请求失败: %v", err), IsError: true}, nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024))
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("读取响应失败: %v", err), IsError: true}, nil
	}

	// 响应头摘要
	var headerSummary strings.Builder
	for _, key := range []string{"Content-Type", "Content-Length", "Location", "Set-Cookie"} {
		if v := resp.Header.Get(key); v != "" {
			headerSummary.WriteString(fmt.Sprintf("%s: %s\n", key, v))
		}
	}

	content := string(respBody)
	if len(content) > 3000 {
		content = content[:3000] + "\n...(响应已截断)"
	}

	return &ToolResult{
		Content: fmt.Sprintf("%s %s\nStatus: %d\n%s\n%s", method, url, resp.StatusCode, headerSummary.String(), content),
	}, nil
}
