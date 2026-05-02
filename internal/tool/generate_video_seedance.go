package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/liteflow/backend/internal/config"
	"github.com/liteflow/backend/internal/platform/events"
	"github.com/liteflow/backend/internal/platform/storage"
)

const (
	defaultSeedanceEndpoint = "https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks"
	defaultSeedanceModel    = "doubao-seedance-2-0-260128"
)

type SeedanceVideoGenerateTool struct {
	storageSvc      storage.Service
	ossLinkSvc      storage.Service
	createArtifact  CreateVideoArtifactFunc
	httpClient      *http.Client
	cfg             config.SeedanceConfig
	allowedPhoneSet map[string]struct{}
}

func NewSeedanceVideoGenerate(
	storageSvc storage.Service,
	ossLinkSvc storage.Service,
	createArtifact CreateVideoArtifactFunc,
	cfg config.SeedanceConfig,
) *SeedanceVideoGenerateTool {
	timeoutSec := cfg.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 300
	}
	allowed := make(map[string]struct{})
	for _, phone := range cfg.AllowedPhones {
		p := strings.TrimSpace(phone)
		if p == "" {
			continue
		}
		allowed[p] = struct{}{}
	}
	return &SeedanceVideoGenerateTool{
		storageSvc:      storageSvc,
		ossLinkSvc:      ossLinkSvc,
		createArtifact:  createArtifact,
		httpClient:      &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
		cfg:             cfg,
		allowedPhoneSet: allowed,
	}
}

func (t *SeedanceVideoGenerateTool) Name() string { return "generate_video_seedance" }

func (t *SeedanceVideoGenerateTool) Description() string {
	return "使用 Seedance（火山方舟 doubao-seedance）生成视频，支持参考图片/视频/音频，返回可下载的视频文件"
}

func (t *SeedanceVideoGenerateTool) IsEnabledForPhone(phone string) bool {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return false
	}
	if len(t.allowedPhoneSet) == 0 {
		return false
	}
	_, ok := t.allowedPhoneSet[phone]
	return ok
}

func (t *SeedanceVideoGenerateTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "视频生成提示词",
			},
			"duration": map[string]any{
				"type":        "integer",
				"minimum":     4,
				"maximum":     15,
				"description": "视频时长（秒），通常 4-15",
			},
			"ratio": map[string]any{
				"type":        "string",
				"enum":        []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"},
				"description": "视频宽高比",
			},
			"resolution": map[string]any{
				"type":        "string",
				"enum":        []string{"480p", "720p", "1080p"},
				"description": "分辨率（仅在模型支持时生效）",
			},
			"generate_audio": map[string]any{
				"type":        "boolean",
				"description": "是否生成音频/旁白",
			},
			"watermark": map[string]any{
				"type":        "boolean",
				"description": "是否添加水印（默认 false）",
			},
			"reference_image_paths": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "参考图路径（对话内路径），会转为临时 URL 作为 reference_image",
			},
			"reference_video_paths": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "参考视频路径（对话内路径），会转为临时 URL 作为 reference_video",
			},
			"reference_audio_paths": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "参考音频路径（对话内路径），会转为临时 URL 作为 reference_audio",
			},
		},
		"required": []string{"prompt"},
	}
}

func (t *SeedanceVideoGenerateTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	if strings.TrimSpace(t.cfg.APIKey) == "" {
		return &ToolResult{Content: "seedance 未配置：请设置 SEEDANCE_API_KEY", IsError: true}, nil
	}
	prompt, _ := input["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return &ToolResult{Content: "prompt is required", IsError: true}, nil
	}
	t.emitProgress(ctx, tc, "已提交视频生成请求，正在调用 Seedance...")

	content := []map[string]any{
		{"type": "text", "text": prompt},
	}
	convID := tc.ConversationID.String()
	t.appendRefURLs(ctx, &content, input, "reference_image_paths", "image_url", "reference_image", convID)
	t.appendRefURLs(ctx, &content, input, "reference_video_paths", "video_url", "reference_video", convID)
	t.appendRefURLs(ctx, &content, input, "reference_audio_paths", "audio_url", "reference_audio", convID)

	model := strings.TrimSpace(t.cfg.Model)
	if model == "" {
		model = defaultSeedanceModel
	}
	reqBody := map[string]any{
		"model":   model,
		"content": content,
	}
	if v, ok := input["generate_audio"].(bool); ok {
		reqBody["generate_audio"] = v
	}
	if v, ok := input["watermark"].(bool); ok {
		reqBody["watermark"] = v
	} else {
		reqBody["watermark"] = false
	}
	if ratio, _ := input["ratio"].(string); strings.TrimSpace(ratio) != "" {
		reqBody["ratio"] = strings.TrimSpace(ratio)
	} else if ratio, _ := input["aspect_ratio"].(string); strings.TrimSpace(ratio) != "" {
		reqBody["ratio"] = strings.TrimSpace(ratio)
	}
	if dur, ok := toInt(input["duration"]); ok {
		reqBody["duration"] = dur
	}
	if res, _ := input["resolution"].(string); strings.TrimSpace(res) != "" {
		reqBody["resolution"] = strings.TrimSpace(res)
	}

	endpoint := strings.TrimSpace(t.cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultSeedanceEndpoint
	}

	rawResp, err := t.doJSON(ctx, http.MethodPost, endpoint, reqBody)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("seedance request failed: %v", err), IsError: true}, nil
	}

	videoURL := firstString(rawResp,
		"video_url",
		"content.video_url",
		"data.video_url",
		"data.content.video_url",
		"result.video_url",
		"data.result.video_url",
	)
	if strings.TrimSpace(videoURL) == "" {
		taskID := firstString(rawResp, "id", "task_id", "data.id", "data.task_id", "result.id", "result.task_id")
		if strings.TrimSpace(taskID) == "" {
			return &ToolResult{Content: "seedance 未返回 video_url 或 task_id", IsError: true}, nil
		}
		t.emitProgress(ctx, tc, fmt.Sprintf("任务已创建（ID: %s），开始轮询生成进度...", taskID))
		videoURL, err = t.pollVideoURL(ctx, endpoint, taskID, tc)
		if err != nil {
			return &ToolResult{Content: fmt.Sprintf("seedance polling failed: %v", err), IsError: true}, nil
		}
	}
	t.emitProgress(ctx, tc, "视频生成完成，正在下载文件...")

	videoBytes, mimeType, err := t.downloadVideo(ctx, videoURL)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("download generated video failed: %v", err), IsError: true}, nil
	}
	filename := "generated_video_" + fmt.Sprintf("%d", time.Now().UnixNano()) + extFromVideoURL(videoURL, mimeType)
	if err := t.storageSvc.UploadFile(ctx, convID, filename, videoBytes); err != nil {
		return &ToolResult{Content: fmt.Sprintf("save generated video failed: %v", err), IsError: true}, nil
	}
	t.emitProgress(ctx, tc, "视频已保存，正在生成可下载结果...")
	metadata, err := t.createArtifact(ctx, tc.ConversationID, tc.MessageID, filename, videoBytes, mimeType)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("create video artifact failed: %v", err), IsError: true}, nil
	}
	signedURL, signErr := t.storageSvc.GeneratePresignedURL(ctx, convID, filename, "GET", 60)
	if signErr == nil && strings.TrimSpace(signedURL) != "" {
		metadata["url"] = signedURL
	}
	return &ToolResult{
		Content:  fmt.Sprintf("视频已生成: %s", filename),
		Metadata: metadata,
	}, nil
}

func (t *SeedanceVideoGenerateTool) appendRefURLs(
	ctx context.Context,
	content *[]map[string]any,
	input map[string]any,
	inputKey, urlField, role, convID string,
) {
	refs, ok := input[inputKey]
	if !ok || t.ossLinkSvc == nil {
		return
	}
	for _, p := range toStringSlice(refs) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		u, err := t.ossLinkSvc.GeneratePresignedURL(ctx, convID, p, "GET", 30)
		if err != nil || strings.TrimSpace(u) == "" {
			continue
		}
		*content = append(*content, map[string]any{
			"type":   urlField,
			urlField: map[string]any{"url": u},
			"role":   role,
		})
	}
}

func (t *SeedanceVideoGenerateTool) pollVideoURL(ctx context.Context, endpoint, taskID string, tc *ToolContext) (string, error) {
	timeoutSec := t.cfg.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 300
	}
	pollSec := t.cfg.PollIntervalSeconds
	if pollSec <= 0 {
		pollSec = 5
	}
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	lastProgressText := ""
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for task %s", taskID)
		}
		statusURL := buildSeedanceStatusURL(endpoint, t.cfg.StatusEndpointTemplate, taskID)
		data, err := t.doJSON(ctx, http.MethodGet, statusURL, nil)
		if err == nil {
			status := strings.ToLower(strings.TrimSpace(firstString(data, "status", "state", "data.status", "data.state", "result.status", "result.state")))
			if progress, ok := firstFloat(data,
				"progress",
				"percent",
				"progress_percent",
				"data.progress",
				"data.percent",
				"data.progress_percent",
				"result.progress",
				"result.percent",
				"result.progress_percent",
			); ok {
				if progress < 0 {
					progress = 0
				}
				if progress > 100 {
					progress = 100
				}
				progressText := fmt.Sprintf("视频生成中：%.0f%%", progress)
				if status != "" {
					progressText += fmt.Sprintf("（%s）", status)
				}
				if progressText != lastProgressText {
					lastProgressText = progressText
					t.emitProgress(ctx, tc, progressText)
				}
			} else if status != "" {
				progressText := fmt.Sprintf("视频生成状态：%s", status)
				if progressText != lastProgressText {
					lastProgressText = progressText
					t.emitProgress(ctx, tc, progressText)
				}
			}

			if videoURL := firstString(data,
				"video_url",
				"content.video_url",
				"data.video_url",
				"data.content.video_url",
				"result.video_url",
				"data.result.video_url",
			); videoURL != "" {
				return videoURL, nil
			}
			if status == "failed" || status == "error" || status == "cancelled" {
				msg := firstString(data, "error.message", "message", "data.error", "result.error")
				if msg == "" {
					msg = "task failed"
				}
				return "", errors.New(msg)
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(pollSec) * time.Second):
		}
	}
}

func (t *SeedanceVideoGenerateTool) emitProgress(ctx context.Context, tc *ToolContext, content string) {
	toolUseID := ""
	if tc != nil {
		toolUseID = tc.ToolUseID
	}
	events.Emit(ctx, events.NewEvent("tool_progress", map[string]any{
		"toolUseId": toolUseID,
		"toolName":  t.Name(),
		"content":   content,
	}))
}

func (t *SeedanceVideoGenerateTool) doJSON(ctx context.Context, method, reqURL string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", withBearer(t.cfg.APIKey))
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return out, nil
}

func (t *SeedanceVideoGenerateTool) downloadVideo(ctx context.Context, videoURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, videoURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("status=%d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024*1024))
	if err != nil {
		return nil, "", err
	}
	mimeType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if mimeType == "" {
		mimeType = "video/mp4"
	}
	return data, mimeType, nil
}

func buildSeedanceStatusURL(endpoint, tpl, taskID string) string {
	if strings.Contains(tpl, "{task_id}") {
		return strings.ReplaceAll(tpl, "{task_id}", taskID)
	}
	base := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	return base + "/" + taskID
}

func withBearer(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(strings.ToLower(v), "bearer ") {
		return v
	}
	return "Bearer " + v
}

func firstString(m map[string]any, paths ...string) string {
	for _, p := range paths {
		if v, ok := getByPath(m, p).(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func getByPath(root any, dotted string) any {
	cur := root
	for _, p := range strings.Split(dotted, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = obj[p]
		if !ok {
			return nil
		}
	}
	return cur
}

func toInt(v any) (int, bool) {
	switch vv := v.(type) {
	case int:
		return vv, true
	case int32:
		return int(vv), true
	case int64:
		return int(vv), true
	case float64:
		return int(vv), true
	default:
		return 0, false
	}
}

func firstFloat(m map[string]any, paths ...string) (float64, bool) {
	for _, p := range paths {
		switch v := getByPath(m, p).(type) {
		case float64:
			return v, true
		case float32:
			return float64(v), true
		case int:
			return float64(v), true
		case int32:
			return float64(v), true
		case int64:
			return float64(v), true
		}
	}
	return 0, false
}

func extFromVideoURL(videoURL, mimeType string) string {
	if strings.Contains(strings.ToLower(mimeType), "webm") {
		return ".webm"
	}
	if strings.Contains(strings.ToLower(mimeType), "quicktime") {
		return ".mov"
	}
	if u, err := url.Parse(videoURL); err == nil {
		ext := strings.ToLower(path.Ext(u.Path))
		if ext == ".mp4" || ext == ".webm" || ext == ".mov" {
			return ext
		}
	}
	return ".mp4"
}
