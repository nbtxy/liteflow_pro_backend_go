package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type MetasoProvider struct {
	apiKey   string
	endpoint string
	timeout  time.Duration
	count    int
}

func NewMetaso(apiKey, endpoint string, timeoutSec, defaultCount int) *MetasoProvider {
	if endpoint == "" {
		endpoint = "https://metaso.cn/api/open"
	}
	return &MetasoProvider{
		apiKey:   apiKey,
		endpoint: endpoint,
		timeout:  time.Duration(timeoutSec) * time.Second,
		count:    defaultCount,
	}
}

func (p *MetasoProvider) Name() string { return "metaso" }

func (p *MetasoProvider) Search(ctx context.Context, query string, count int) ([]Result, error) {
	if count <= 0 {
		count = p.count
	}

	reqBody := map[string]any{
		"query": query,
		"count": count,
	}
	jsonBody, _ := json.Marshal(reqBody)

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint+"/search", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("metaso API error %d: %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Data struct {
			Items []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Content string `json:"content"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	results := make([]Result, 0, len(apiResp.Data.Items))
	for _, item := range apiResp.Data.Items {
		results = append(results, Result{
			Title:   item.Title,
			URL:     item.URL,
			Content: item.Content,
		})
	}
	return results, nil
}
