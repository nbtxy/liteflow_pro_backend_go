package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type ServerConfig struct {
	ServerURL string
	Token     string
}

type resolvedAuth struct {
	token      string
	isFeishu   bool
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type ToolResult struct {
	Content string
	IsError bool
}

func (c *Client) ListTools(ctx context.Context, cfg ServerConfig) ([]ToolDefinition, error) {
	sessionID, err := c.initializeSession(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("MCP initialize failed: %w", err)
	}

	result, err := c.jsonRpcCall(ctx, cfg, sessionID, "tools/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("tools/list failed: %w", err)
	}

	toolsNode, ok := result["tools"]
	if !ok {
		return nil, nil
	}

	toolsBytes, err := json.Marshal(toolsNode)
	if err != nil {
		return nil, fmt.Errorf("marshal tools: %w", err)
	}

	var defs []ToolDefinition
	if err := json.Unmarshal(toolsBytes, &defs); err != nil {
		return nil, fmt.Errorf("unmarshal tools: %w", err)
	}

	return defs, nil
}

func (c *Client) CallTool(ctx context.Context, cfg ServerConfig, toolName string, arguments map[string]any) (*ToolResult, error) {
	sessionID, err := c.initializeSession(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("MCP initialize failed: %w", err)
	}

	result, err := c.jsonRpcCall(ctx, cfg, sessionID, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": arguments,
	})
	if err != nil {
		return nil, err
	}

	var content string
	if contentNode, ok := result["content"]; ok {
		contentBytes, _ := json.Marshal(contentNode)
		if items, ok := contentNode.([]any); ok {
			var sb strings.Builder
			for _, item := range items {
				if m, ok := item.(map[string]any); ok {
					if text, ok := m["text"].(string); ok {
						sb.WriteString(text)
					} else {
						b, _ := json.Marshal(m)
						sb.WriteString(string(b))
					}
				}
			}
			content = sb.String()
		} else {
			content = string(contentBytes)
		}
	}

	isError := false
	if v, ok := result["isError"].(bool); ok {
		isError = v
	}

	return &ToolResult{Content: content, IsError: isError}, nil
}

// resolveToken handles Feishu-specific token resolution.
// Feishu stores token as "appId:appSecret" and requires fetching a real-time tenant_access_token.
func (c *Client) resolveToken(ctx context.Context, cfg ServerConfig) (*resolvedAuth, error) {
	if cfg.Token == "" {
		return &resolvedAuth{}, nil
	}

	isFeishu := strings.Contains(cfg.ServerURL, "feishu.cn") && strings.Contains(cfg.Token, ":")
	if !isFeishu {
		return &resolvedAuth{token: cfg.Token}, nil
	}

	parts := strings.SplitN(cfg.Token, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("飞书 token 格式错误，需要 appId:appSecret")
	}
	appID, appSecret := parts[0], parts[1]

	reqBody, _ := json.Marshal(map[string]string{
		"app_id":     appID,
		"app_secret": appSecret,
	})

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal",
		bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取飞书 token 失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析飞书 token 响应失败: %w", err)
	}

	code, _ := result["code"].(float64)
	if int(code) != 0 {
		return nil, fmt.Errorf("获取飞书 token 失败: %s", string(body))
	}

	tat, _ := result["tenant_access_token"].(string)
	if tat == "" {
		return nil, fmt.Errorf("飞书 token 响应缺少 tenant_access_token")
	}

	slog.Info("Feishu tenant_access_token obtained", "token_len", len(tat))
	return &resolvedAuth{token: tat, isFeishu: true}, nil
}

func (c *Client) setAuthHeaders(req *http.Request, auth *resolvedAuth) {
	if auth == nil || auth.token == "" {
		return
	}
	if auth.isFeishu {
		req.Header.Set("X-Lark-MCP-TAT", auth.token)
		req.Header.Set("X-Lark-MCP-Allowed-Tools", "search-user,get-user,fetch-file,search-doc,create-doc,update-doc,list-docs,get-comments,add-comments")
	} else {
		req.Header.Set("Authorization", "Bearer "+auth.token)
	}
}

func (c *Client) initializeSession(ctx context.Context, cfg ServerConfig) (string, error) {
	slog.Info("MCP initialize", "server", cfg.ServerURL)

	auth, err := c.resolveToken(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("resolve token: %w", err)
	}

	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "liteflow-backend",
				"version": "1.0.0",
			},
		},
		"id": uuid.NewString(),
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.ServerURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	c.setAuthHeaders(req, auth)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("connect to MCP server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("MCP initialize HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	sessionID := resp.Header.Get("Mcp-Session-Id")

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		_, err = c.readSseResult(resp.Body, reqBody["id"].(string))
	} else {
		var jsonResp map[string]any
		err = json.NewDecoder(resp.Body).Decode(&jsonResp)
		if err == nil {
			_, err = parseJsonRpcResult(jsonResp)
		}
	}
	if err != nil {
		return "", err
	}

	// Send initialized notification
	c.sendNotification(ctx, cfg, auth, sessionID, "notifications/initialized", map[string]any{})

	slog.Info("MCP session initialized", "server", cfg.ServerURL, "hasSession", sessionID != "")
	return sessionID, nil
}

func (c *Client) jsonRpcCall(ctx context.Context, cfg ServerConfig, sessionID, method string, params map[string]any) (map[string]any, error) {
	auth, err := c.resolveToken(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve token: %w", err)
	}

	requestID := uuid.NewString()
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
		"id":      requestID,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.ServerURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	c.setAuthHeaders(req, auth)

	slog.Info("MCP call", "method", method, "server", cfg.ServerURL)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("认证失败，请检查令牌是否有效")
		}
		return nil, fmt.Errorf("MCP HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		return c.readSseResult(resp.Body, requestID)
	}

	var jsonResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&jsonResp); err != nil {
		return nil, fmt.Errorf("parse MCP response: %w", err)
	}

	return parseJsonRpcResult(jsonResp)
}

func (c *Client) sendNotification(ctx context.Context, cfg ServerConfig, auth *resolvedAuth, sessionID, method string, params map[string]any) {
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}

	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.ServerURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	c.setAuthHeaders(req, auth)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Warn("MCP notification failed", "method", method, "err", err)
		return
	}
	resp.Body.Close()
}

func (c *Client) readSseResult(body io.Reader, requestID string) (map[string]any, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 5*1024*1024)

	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()

		// Empty line = end of an event; process accumulated data
		if line == "" {
			if len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")
				dataLines = nil

				var jsonResp map[string]any
				if err := json.Unmarshal([]byte(data), &jsonResp); err != nil {
					slog.Debug("SSE skip non-JSON data", "len", len(data))
					continue
				}

				if id, ok := jsonResp["id"].(string); ok && id == requestID {
					return parseJsonRpcResult(jsonResp)
				}
				if _, hasResult := jsonResp["result"]; hasResult {
					return parseJsonRpcResult(jsonResp)
				}
				if _, hasErr := jsonResp["error"]; hasErr {
					return parseJsonRpcResult(jsonResp)
				}
			}
			continue
		}

		// Accumulate data lines
		if strings.HasPrefix(line, "data:") {
			d := strings.TrimPrefix(line, "data:")
			d = strings.TrimPrefix(d, " ")
			dataLines = append(dataLines, d)
		}
		// Skip event:, id:, retry: etc.
	}

	// Handle trailing data without final blank line
	if len(dataLines) > 0 {
		data := strings.Join(dataLines, "\n")
		var jsonResp map[string]any
		if err := json.Unmarshal([]byte(data), &jsonResp); err == nil {
			if id, ok := jsonResp["id"].(string); ok && id == requestID {
				return parseJsonRpcResult(jsonResp)
			}
			if _, hasResult := jsonResp["result"]; hasResult {
				return parseJsonRpcResult(jsonResp)
			}
			if _, hasErr := jsonResp["error"]; hasErr {
				return parseJsonRpcResult(jsonResp)
			}
		}
	}

	return nil, fmt.Errorf("no JSON-RPC response found in SSE stream")
}

func parseJsonRpcResult(resp map[string]any) (map[string]any, error) {
	if errField, ok := resp["error"]; ok {
		if errMap, ok := errField.(map[string]any); ok {
			msg, _ := errMap["message"].(string)
			if msg == "" {
				msg = "unknown MCP error"
			}
			return nil, fmt.Errorf("MCP error: %s", msg)
		}
	}

	result, ok := resp["result"]
	if !ok {
		return nil, fmt.Errorf("MCP response missing 'result' field")
	}

	if resultMap, ok := result.(map[string]any); ok {
		return resultMap, nil
	}

	return map[string]any{"value": result}, nil
}
