package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Mode string

const (
	ModeOff              Mode = "off"
	RecordOnly           Mode = "record"
	ReplayOnly           Mode = "replay"
	defaultFixtureSubdir      = "storage/llm_requests"
)

const (
	replaySchemaV2    = "v2"
	orphanDirName     = "_orphan"
	streamFlushBytes  = 16 * 1024
	streamFlushWindow = 100 * time.Millisecond
)

var errFixtureMiss = errors.New("llm replay fixture not found")

func IsFixtureMiss(err error) bool {
	return errors.Is(err, errFixtureMiss)
}

func ParseRecordMode(raw string) Mode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "record":
		return RecordOnly
	case "off":
		return ModeOff
	case "replay":
		return ReplayOnly
	default:
		return RecordOnly
	}
}

func IsReplayMode(mode Mode) bool {
	return mode == ReplayOnly
}

func DefaultFixtureDir() string {
	if dir := strings.TrimSpace(os.Getenv("LLM_SAVE_REQUEST_DIR")); dir != "" {
		return dir
	}
	return "." + string(filepath.Separator) + defaultFixtureSubdir
}

func replayForceIDsFromEnv(mode Mode) (string, string, bool) {
	if !IsReplayMode(mode) {
		return "", "", false
	}
	convID := strings.TrimSpace(os.Getenv("LLM_REPLAY_FORCE_CONV_ID"))
	msgID := strings.TrimSpace(os.Getenv("LLM_REPLAY_FORCE_MSG_ID"))
	if convID == "" || msgID == "" {
		return "", "", false
	}
	return convID, msgID, true
}

func ReplayForceIDsForMode(mode Mode) (string, string, bool) {
	return replayForceIDsFromEnv(mode)
}

type RecordingProvider struct {
	inner      Provider
	fixtureDir string
	mode       Mode

	mu            sync.Mutex
	orphanCursors map[string]int
}

func NewRecordingProvider(inner Provider, fixtureDir string, mode Mode) *RecordingProvider {
	if strings.TrimSpace(fixtureDir) == "" {
		fixtureDir = DefaultFixtureDir()
	}
	return &RecordingProvider{
		inner:         inner,
		fixtureDir:    fixtureDir,
		mode:          mode,
		orphanCursors: make(map[string]int),
	}
}

func (p *RecordingProvider) Name() string {
	if p.inner == nil {
		return "recording"
	}
	return p.inner.Name()
}

func (p *RecordingProvider) MaxContextTokens() int {
	if p.inner == nil {
		return 0
	}
	return p.inner.MaxContextTokens()
}

func (p *RecordingProvider) SupportsVision() bool {
	if p.inner == nil {
		return false
	}
	return p.inner.SupportsVision()
}

func (p *RecordingProvider) StreamChat(ctx context.Context, req *LlmRequest) (<-chan LlmChunk, error) {
	switch p.mode {
	case ReplayOnly:
		return p.replayStream(ctx, req)
	case RecordOnly:
		return p.recordStream(ctx, req)
	default:
		if p.inner == nil {
			return nil, errors.New("recording provider disabled and inner provider is nil")
		}
		return p.inner.StreamChat(ctx, req)
	}
}

func (p *RecordingProvider) Chat(ctx context.Context, req *LlmRequest) (*LlmResponse, error) {
	switch p.mode {
	case ReplayOnly:
		return p.replayChat(ctx, req)
	case RecordOnly:
		return p.recordChat(ctx, req)
	default:
		if p.inner == nil {
			return nil, errors.New("recording provider disabled and inner provider is nil")
		}
		return p.inner.Chat(ctx, req)
	}
}

type replayResponse struct {
	Chunks     []replayChunk `json:"chunks,omitempty"`
	ChunksFile string        `json:"chunks_file,omitempty"`
	ChunkCount int           `json:"chunk_count,omitempty"`
	Completion *LlmResponse  `json:"completion,omitempty"`
}

type replayChunk struct {
	Content              string    `json:"content,omitempty"`
	ToolCallIndex        int       `json:"tool_call_index,omitempty"`
	ToolCallID           string    `json:"tool_call_id,omitempty"`
	ToolCallFunctionName string    `json:"tool_call_function_name,omitempty"`
	ToolCallFunctionArgs string    `json:"tool_call_function_args,omitempty"`
	Usage                *LlmUsage `json:"usage,omitempty"`
	FinishReason         string    `json:"finish_reason,omitempty"`
	Error                string    `json:"error,omitempty"`
}

func replayChunkFromLlm(c LlmChunk) replayChunk {
	rc := replayChunk{
		Content:              c.Content,
		ToolCallIndex:        c.ToolCallIndex,
		ToolCallID:           c.ToolCallID,
		ToolCallFunctionName: c.ToolCallFunctionName,
		ToolCallFunctionArgs: c.ToolCallFunctionArgs,
		Usage:                c.Usage,
		FinishReason:         c.FinishReason,
	}
	if c.Err != nil {
		rc.Error = c.Err.Error()
	}
	return rc
}

func (c replayChunk) toLlmChunk() LlmChunk {
	out := LlmChunk{
		Content:              c.Content,
		ToolCallIndex:        c.ToolCallIndex,
		ToolCallID:           c.ToolCallID,
		ToolCallFunctionName: c.ToolCallFunctionName,
		ToolCallFunctionArgs: c.ToolCallFunctionArgs,
		Usage:                c.Usage,
		FinishReason:         c.FinishReason,
	}
	if strings.TrimSpace(c.Error) != "" {
		out.Err = errors.New(c.Error)
	}
	return out
}

type replayRecord struct {
	Schema         string          `json:"schema,omitempty"`
	SavedAt        string          `json:"saved_at,omitempty"`
	ConversationID string          `json:"conversation_id,omitempty"`
	UserMessageID  string          `json:"user_message_id,omitempty"`
	CallSeq        int             `json:"call_seq,omitempty"`
	Provider       string          `json:"provider,omitempty"`
	Endpoint       string          `json:"endpoint,omitempty"`
	Mode           string          `json:"mode,omitempty"`
	Request        json.RawMessage `json:"request,omitempty"`
	Response       replayResponse  `json:"response,omitempty"`
	DurationMS     int64           `json:"duration_ms,omitempty"`
	Error          *string         `json:"error,omitempty"`
}

type replayManifest struct {
	Schema         string `json:"schema"`
	ConversationID string `json:"conversation_id"`
	UserMessageID  string `json:"user_message_id"`
	AgentID        string `json:"agent_id,omitempty"`
	CallCount      int    `json:"call_count"`
	FirstSavedAt   string `json:"first_saved_at,omitempty"`
	LastSavedAt    string `json:"last_saved_at,omitempty"`
}

func (p *RecordingProvider) replayStream(ctx context.Context, _ *LlmRequest) (<-chan LlmChunk, error) {
	record, recordPath, err := p.loadReplayFixture(ctx, "stream")
	if err != nil {
		return nil, err
	}
	if record.Error != nil {
		ch := make(chan LlmChunk, 1)
		ch <- LlmChunk{Err: errors.New(*record.Error)}
		close(ch)
		return ch, nil
	}

	ch := make(chan LlmChunk, 64)
	go func() {
		defer close(ch)
		if record.Response.ChunksFile != "" {
			if err := p.emitChunksFromFile(ctx, filepath.Join(filepath.Dir(recordPath), record.Response.ChunksFile), ch); err != nil {
				select {
				case ch <- LlmChunk{Err: err}:
				case <-ctx.Done():
				}
			}
			return
		}
		for _, chunk := range record.Response.Chunks {
			select {
			case ch <- chunk.toLlmChunk():
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (p *RecordingProvider) replayChat(ctx context.Context, _ *LlmRequest) (*LlmResponse, error) {
	record, _, err := p.loadReplayFixture(ctx, "chat")
	if err != nil {
		return nil, err
	}
	if record.Error != nil {
		return nil, errors.New(*record.Error)
	}
	if record.Response.Completion == nil {
		return nil, errors.New("replay fixture missing completion")
	}
	return record.Response.Completion, nil
}

func (p *RecordingProvider) recordStream(ctx context.Context, req *LlmRequest) (<-chan LlmChunk, error) {
	if p.inner == nil {
		return nil, errors.New("record mode requires inner provider")
	}

	meta := p.newRecordMeta(ctx, "stream", req)
	rec, recErr := p.prepareRecorder(meta)

	start := time.Now()
	chunks, err := p.inner.StreamChat(ctx, req)
	if err != nil {
		meta.DurationMS = time.Since(start).Milliseconds()
		msg := err.Error()
		meta.Error = &msg
		if recErr == nil {
			_ = rec.finalize(meta)
		}
		return nil, err
	}

	out := make(chan LlmChunk, 64)
	go func() {
		defer close(out)
		var streamErr error
		for chunk := range chunks {
			if recErr == nil {
				if err := rec.appendChunk(chunk); err != nil && streamErr == nil {
					streamErr = err
				}
			}
			select {
			case out <- chunk:
			case <-ctx.Done():
				streamErr = ctx.Err()
				goto done
			}
			if chunk.Err != nil && streamErr == nil {
				streamErr = chunk.Err
			}
		}
	done:
		meta.DurationMS = time.Since(start).Milliseconds()
		if streamErr != nil {
			msg := streamErr.Error()
			meta.Error = &msg
		}
		if recErr == nil {
			_ = rec.finalize(meta)
		}
	}()
	return out, nil
}

func (p *RecordingProvider) recordChat(ctx context.Context, req *LlmRequest) (*LlmResponse, error) {
	if p.inner == nil {
		return nil, errors.New("record mode requires inner provider")
	}

	meta := p.newRecordMeta(ctx, "chat", req)
	start := time.Now()
	resp, err := p.inner.Chat(ctx, req)
	meta.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		msg := err.Error()
		meta.Error = &msg
	} else {
		meta.Response.Completion = resp
	}

	rec, recErr := p.prepareRecorder(meta)
	if recErr == nil {
		_ = rec.finalize(meta)
	}
	return resp, err
}

func (p *RecordingProvider) newRecordMeta(ctx context.Context, mode string, req *LlmRequest) replayRecord {
	reqBytes, _ := json.Marshal(req)
	cc := p.callContextForIO(ctx)
	return replayRecord{
		Schema:         replaySchemaV2,
		ConversationID: cc.ConversationID,
		UserMessageID:  cc.UserMessageID,
		CallSeq:        cc.CallSeq,
		Provider:       p.Name(),
		Endpoint:       providerEndpoint(p.inner),
		Mode:           mode,
		Request:        reqBytes,
	}
}

func (p *RecordingProvider) callContextForIO(ctx context.Context) CallContext {
	cc, _ := CallContextFromCtx(ctx)
	if conv, msg, ok := replayForceIDsFromEnv(p.mode); ok {
		cc.ConversationID = conv
		cc.UserMessageID = msg
	}
	return cc
}

func providerEndpoint(p Provider) string {
	type endpointProvider interface {
		Endpoint() string
	}
	if ep, ok := p.(endpointProvider); ok {
		return ep.Endpoint()
	}
	return ""
}

type streamRecorder struct {
	parent         *RecordingProvider
	recordPathTmp  string
	recordPath     string
	chunksPathTmp  string
	chunksPath     string
	chunkWriter    *bufio.Writer
	chunkFile      *os.File
	lastFlush      time.Time
	sinceFlushByte int
	chunkCount     int
}

func (p *RecordingProvider) prepareRecorder(meta replayRecord) (*streamRecorder, error) {
	recordPath, chunksPath, err := p.fixturePaths(meta)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(recordPath), 0o755); err != nil {
		return nil, err
	}

	rec := &streamRecorder{
		parent:        p,
		recordPath:    recordPath,
		recordPathTmp: recordPath + ".tmp",
		lastFlush:     time.Now(),
	}
	if meta.Mode == "stream" {
		rec.chunksPath = chunksPath
		rec.chunksPathTmp = chunksPath + ".tmp"
		f, err := os.OpenFile(rec.chunksPathTmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		rec.chunkFile = f
		rec.chunkWriter = bufio.NewWriterSize(f, 32*1024)
	}
	return rec, nil
}

func (r *streamRecorder) appendChunk(chunk LlmChunk) error {
	if r.chunkWriter == nil {
		return nil
	}
	b, err := json.Marshal(replayChunkFromLlm(chunk))
	if err != nil {
		return err
	}
	if _, err := r.chunkWriter.Write(b); err != nil {
		return err
	}
	if err := r.chunkWriter.WriteByte('\n'); err != nil {
		return err
	}
	r.chunkCount++
	r.sinceFlushByte += len(b) + 1
	if r.sinceFlushByte >= streamFlushBytes || time.Since(r.lastFlush) >= streamFlushWindow {
		if err := r.chunkWriter.Flush(); err != nil {
			return err
		}
		r.lastFlush = time.Now()
		r.sinceFlushByte = 0
	}
	return nil
}

func (r *streamRecorder) finalize(meta replayRecord) error {
	meta.SavedAt = time.Now().Format(time.RFC3339Nano)
	if meta.Mode == "stream" {
		meta.Response.ChunksFile = filepath.Base(r.chunksPath)
		meta.Response.ChunkCount = r.chunkCount
		meta.Response.Completion = nil
	}
	content, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(r.recordPathTmp, content, 0o644); err != nil {
		return err
	}
	if r.chunkWriter != nil {
		if err := r.chunkWriter.Flush(); err != nil {
			return err
		}
		if err := r.chunkFile.Close(); err != nil {
			return err
		}
		if err := os.Rename(r.chunksPathTmp, r.chunksPath); err != nil {
			return err
		}
	}
	if err := os.Rename(r.recordPathTmp, r.recordPath); err != nil {
		return err
	}
	_ = r.parent.updateManifest(meta)
	return nil
}

func (p *RecordingProvider) fixturePaths(meta replayRecord) (recordPath string, chunksPath string, err error) {
	baseDir := p.fixtureDir
	fileName := ""
	if meta.ConversationID != "" && meta.UserMessageID != "" {
		baseDir = filepath.Join(baseDir, meta.ConversationID, meta.UserMessageID)
		fileName = fmt.Sprintf("%02d_%s_%s.json", meta.CallSeq, sanitizeFileName(meta.Provider), meta.Mode)
	} else {
		baseDir = filepath.Join(baseDir, orphanDirName)
		fileName = fmt.Sprintf("%s_%s_%d.json", sanitizeFileName(meta.Provider), meta.Mode, time.Now().UnixNano())
	}
	if meta.Mode == "stream" {
		chunksPath = filepath.Join(baseDir, strings.TrimSuffix(fileName, ".json")+".chunks.jsonl")
	}
	return filepath.Join(baseDir, fileName), chunksPath, nil
}

func sanitizeFileName(v string) string {
	if v == "" {
		return "unknown"
	}
	v = strings.ReplaceAll(v, "/", "_")
	v = strings.ReplaceAll(v, " ", "_")
	return v
}

func (p *RecordingProvider) loadReplayFixture(ctx context.Context, mode string) (*replayRecord, string, error) {
	cc := p.callContextForIO(ctx)
	if cc.ConversationID != "" && cc.UserMessageID != "" {
		path, err := p.findMessageFixturePath(cc, mode)
		if err != nil {
			return nil, "", err
		}
		record, err := readReplayRecord(path)
		if err != nil {
			return nil, "", err
		}
		return record, path, nil
	}

	path, err := p.consumeOrphanFixturePath(mode)
	if err != nil {
		return nil, "", err
	}
	record, err := readReplayRecord(path)
	if err != nil {
		return nil, "", err
	}
	return record, path, nil
}

func (p *RecordingProvider) findMessageFixturePath(cc CallContext, mode string) (string, error) {
	dir := filepath.Join(p.fixtureDir, cc.ConversationID, cc.UserMessageID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", errFixtureMiss, dir)
		}
		return "", err
	}
	seqPrefix := fmt.Sprintf("%02d_", cc.CallSeq)
	exactName := seqPrefix + sanitizeFileName(p.Name()) + "_" + mode + ".json"

	// Prefer exact provider match, then fall back to same seq/mode file.
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if e.Name() == exactName {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, ".tmp") || !strings.HasSuffix(name, ".json") {
			continue
		}
		if strings.HasPrefix(name, seqPrefix) && strings.HasSuffix(name, "_"+mode+".json") {
			return filepath.Join(dir, name), nil
		}
	}
	return "", fmt.Errorf("%w: %s/%s seq=%d mode=%s", errFixtureMiss, cc.ConversationID, cc.UserMessageID, cc.CallSeq, mode)
}

func (p *RecordingProvider) consumeOrphanFixturePath(mode string) (string, error) {
	dir := filepath.Join(p.fixtureDir, orphanDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: orphan dir missing", errFixtureMiss)
		}
		return "", err
	}

	providerName := sanitizeFileName(p.Name())
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") || !strings.HasSuffix(name, ".json") {
			continue
		}
		if strings.HasPrefix(name, providerName+"_"+mode+"_") {
			files = append(files, filepath.Join(dir, name))
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return "", fmt.Errorf("%w: no orphan fixture for %s/%s", errFixtureMiss, providerName, mode)
	}

	key := providerName + "|" + mode
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := p.orphanCursors[key]
	if idx >= len(files) {
		return "", fmt.Errorf("%w: orphan fixture exhausted for %s/%s", errFixtureMiss, providerName, mode)
	}
	p.orphanCursors[key] = idx + 1
	return files[idx], nil
}

func readReplayRecord(path string) (*replayRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec replayRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (p *RecordingProvider) emitChunksFromFile(ctx context.Context, path string, out chan<- LlmChunk) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sawToolCallDelta := false
	sawToolCallsFinish := false
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var wire replayChunk
		if err := json.Unmarshal(line, &wire); err != nil {
			return err
		}
		chunk := wire.toLlmChunk()
		if chunk.HasToolCallDelta() {
			sawToolCallDelta = true
		}
		if chunk.FinishReason == "tool_calls" {
			sawToolCallsFinish = true
		}
		select {
		case out <- chunk:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	// Backward compatibility: early fixtures stored stream chunks without
	// tool-call delta fields (because LlmChunk tool fields were json:"-"),
	// which makes agent loop stop before tool execution. If we saw
	// finish_reason=tool_calls but no tool delta, synthesize from tool fixtures.
	if !sawToolCallDelta && sawToolCallsFinish {
		if err := p.emitSyntheticToolCallChunks(ctx, path, out); err != nil {
			return err
		}
	}
	return nil
}

type replayToolFixture struct {
	ToolSeq    int            `json:"tool_seq"`
	ToolCallID string         `json:"tool_call_id"`
	ToolName   string         `json:"tool_name"`
	Input      map[string]any `json:"input"`
}

func (p *RecordingProvider) emitSyntheticToolCallChunks(ctx context.Context, chunkFilePath string, out chan<- LlmChunk) error {
	dir := filepath.Dir(chunkFilePath)
	base := filepath.Base(chunkFilePath)
	if len(base) < 2 {
		return nil
	}
	seqPrefix := base[:2]
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	files := make([]string, 0, 4)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, ".tmp") || !strings.HasSuffix(name, ".json") {
			continue
		}
		if strings.HasPrefix(name, seqPrefix+"_tool_") {
			files = append(files, filepath.Join(dir, name))
		}
	}
	sort.Strings(files)
	for i, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		var tf replayToolFixture
		if err := json.Unmarshal(raw, &tf); err != nil {
			return err
		}
		argsBytes, _ := json.Marshal(tf.Input)
		chunk := LlmChunk{
			ToolCallIndex:        tf.ToolSeq,
			ToolCallID:           tf.ToolCallID,
			ToolCallFunctionName: tf.ToolName,
			ToolCallFunctionArgs: string(argsBytes),
		}
		if i == len(files)-1 {
			chunk.FinishReason = "tool_calls"
		}
		select {
		case out <- chunk:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (p *RecordingProvider) updateManifest(meta replayRecord) error {
	if meta.ConversationID == "" || meta.UserMessageID == "" {
		return nil
	}
	manifestPath := filepath.Join(p.fixtureDir, meta.ConversationID, meta.UserMessageID, "manifest.json")
	manifest := replayManifest{
		Schema:         replaySchemaV2,
		ConversationID: meta.ConversationID,
		UserMessageID:  meta.UserMessageID,
		CallCount:      meta.CallSeq + 1,
		FirstSavedAt:   meta.SavedAt,
		LastSavedAt:    meta.SavedAt,
	}
	if b, err := os.ReadFile(manifestPath); err == nil {
		var existing replayManifest
		if json.Unmarshal(b, &existing) == nil {
			manifest = existing
			if manifest.Schema == "" {
				manifest.Schema = replaySchemaV2
			}
			if manifest.ConversationID == "" {
				manifest.ConversationID = meta.ConversationID
			}
			if manifest.UserMessageID == "" {
				manifest.UserMessageID = meta.UserMessageID
			}
			if manifest.CallCount < meta.CallSeq+1 {
				manifest.CallCount = meta.CallSeq + 1
			}
			if manifest.FirstSavedAt == "" || meta.SavedAt < manifest.FirstSavedAt {
				manifest.FirstSavedAt = meta.SavedAt
			}
			if meta.SavedAt > manifest.LastSavedAt {
				manifest.LastSavedAt = meta.SavedAt
			}
		}
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath, data, 0o644)
}
