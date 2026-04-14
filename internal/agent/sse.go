package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	// 取消该连接的写超时，避免长时间 SSE 流被服务器中断
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		slog.Warn("failed to disable write deadline for SSE", "err", err)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	return &SSEWriter{w: w, flusher: flusher}, nil
}

func (s *SSEWriter) SendEvent(event Event) {
	data, err := json.Marshal(event.Data)
	if err != nil {
		slog.Error("failed to marshal SSE event", "err", err)
		return
	}

	fmt.Fprintf(s.w, "data: %s\n\n", data)
	s.flusher.Flush()
}

func (s *SSEWriter) SendEvents(events <-chan Event) {
	for event := range events {
		s.SendEvent(event)
	}
}
