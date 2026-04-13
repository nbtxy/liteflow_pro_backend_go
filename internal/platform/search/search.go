package search

import "context"

type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

type Provider interface {
	Name() string
	Search(ctx context.Context, query string, count int) ([]Result, error)
}
