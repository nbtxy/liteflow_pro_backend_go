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

type TavilyProvider struct {
	apiKey   string
	endpoint string
	timeout  time.Duration
	count    int
}

func NewTavily(apiKey, endpoint string, timeoutSec, defaultCount int) *TavilyProvider {
	if endpoint == "" {
		endpoint = "https://api.tavily.com"
	}
	return &TavilyProvider{
		apiKey:   apiKey,
		endpoint: endpoint,
		timeout:  time.Duration(timeoutSec) * time.Second,
		count:    defaultCount,
	}
}

func (p *TavilyProvider) Name() string { return "tavily" }

func (p *TavilyProvider) Search(ctx context.Context, query string, count int) ([]Result, error) {
	if count <= 0 {
		count = p.count
	}

	reqBody := map[string]any{
		"api_key":            p.apiKey,
		"query":              query,
		"max_results":        count,
		"include_answer":     false,
		"include_raw_content": false,
	}
	jsonBody, _ := json.Marshal(reqBody)

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint+"/search", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tavily API error %d: %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	results := make([]Result, 0, len(apiResp.Results))
	for _, item := range apiResp.Results {
		results = append(results, Result{
			Title:   item.Title,
			URL:     item.URL,
			Content: item.Content,
		})
	}
	return results, nil
}
