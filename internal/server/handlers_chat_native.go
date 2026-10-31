package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yaping-pro/agyapi/internal/antigravity"
	"github.com/yaping-pro/agyapi/internal/auth"
	"github.com/yaping-pro/agyapi/internal/config"
	"github.com/yaping-pro/agyapi/internal/convert"
	"github.com/yaping-pro/agyapi/internal/openai"
)

// nativeKeepAliveInterval bounds how long a client may see a silent stream before a
// comment frame proves the connection is still alive.
const nativeKeepAliveInterval = 15 * time.Second

// upstreamErrorEnvelope catches in-band error frames returned with HTTP 200.
type upstreamErrorEnvelope struct {
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// newNativeClient wires the token store and HTTP client for the native backend.
func newNativeClient(cfg *config.Config) *antigravity.Client {
	tokens := auth.NewStore(cfg.TokenPath)
	tokens.TokenURL = cfg.OAuthTokenURL
	tokens.ClientID = cfg.OAuthClientID
	tokens.ClientSecret = cfg.OAuthClientSecret
	return antigravity.NewClient(cfg.UpstreamURL, cfg.Project, tokens)
}

// handleChatNative serves /v1/chat/completions by calling the Cloud Code Assist
// v1internal API directly. No subprocess, no prompt compilation, no Keychain.
func handleChatNative(cfg *config.Config) http.HandlerFunc {
	client := newNativeClient(cfg)

	return func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()
		r.Body = http.MaxBytesReader(w, r.Body, 16<<20)

		var req openai.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON payload: "+err.Error(), "invalid_request_error")
			return
		}
		if len(req.Messages) == 0 {
			writeJSONError(w, http.StatusBadRequest, "messages array cannot be empty", "invalid_request_error")
			return
		}

		cleanupImages, err := openai.MaterializeImages(req.Messages)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "failed to process images: "+err.Error(), "invalid_request_error")
			return
		}
		defer cleanupImages()

		model := req.Model
		if model == "" {
			model = cfg.DefaultModel
		}
		meta := convert.ChunkMeta{
			ID:      generateChatID(),
			Created: time.Now().Unix(),
			Model:   model,
		}

		upReq, err := convert.BuildRequest(req, convert.BuildOptions{
			Project:   cfg.Project,
			RequestID: antigravity.RequestID(),
			SessionID: antigravity.BuildSessionID(firstUserText(req.Messages)),
		})
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
			return
		}

		if req.Stream {
			streamNative(w, r, client, upReq, meta, startTime)
			return
		}
		nonStreamNative(w, r, client, upReq, meta, startTime)
	}
}

// streamNative relays upstream SSE as OpenAI chunks.
func streamNative(w http.ResponseWriter, r *http.Request, client *antigravity.Client, upReq *antigravity.GenerateRequest, meta convert.ChunkMeta, startTime time.Time) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "Streaming unsupported", "server_error")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sink := newSSESink(w, flusher)

	// Announce the assistant role once, exactly like the legacy bridge did.
	sink.chunk(openai.ChatCompletionChunk{
		ID:      meta.ID,
		Object:  "chat.completion.chunk",
		Created: meta.Created,
		Model:   meta.Model,
		Choices: []openai.ChunkChoice{{
			Index: 0,
			Delta: openai.ChunkDelta{Role: "assistant"},
		}},
	})

	stopKeepAlive := sink.startKeepAlive()
	defer stopKeepAlive()

	body, err := client.StreamGenerate(r.Context(), upReq)
	if err != nil {
		slog.Error("native upstream error", "error", err)
		sink.inBandError(err)
		sink.finish(meta, "stop", nil)
		sink.done()
		return
	}
	defer body.Close()

	sse := antigravity.NewSSE(body)
	toolCalls := 0
	for {
		payload, err := sse.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if r.Context().Err() == nil {
				slog.Error("native SSE read error", "error", err)
				sink.inBandError(err)
			}
			sink.finish(meta, "stop", nil)
			sink.done()
			return
		}

		if upErr := decodeUpstreamError(payload); upErr != nil {
			slog.Error("native upstream in-band error", "error", upErr)
			sink.inBandError(upErr)
			sink.finish(meta, "stop", nil)
			sink.done()
			return
		}

		var env antigravity.StreamEnvelope
		if err := json.Unmarshal(payload, &env); err != nil || env.Response == nil {
			continue
		}

		chunks, finish, usage := convert.ConvertChunk(meta, env.Response)
		for _, c := range chunks {
			toolCalls += len(c.Choices[0].Delta.ToolCalls)
			sink.chunk(c)
		}
		if finish != "" {
			sink.finish(meta, finish, usage)
			sink.done()
			slog.Info("native chat stream completed",
				"model", meta.Model,
				"duration", time.Since(startTime),
				"tool_calls", toolCalls,
				"total_tokens", usageTotal(usage),
			)
			return
		}
	}

	sink.finish(meta, "stop", nil)
	sink.done()
}

// nonStreamNative drains the upstream stream into a single chat.completion.
func nonStreamNative(w http.ResponseWriter, r *http.Request, client *antigravity.Client, upReq *antigravity.GenerateRequest, meta convert.ChunkMeta, startTime time.Time) {
	body, err := client.StreamGenerate(r.Context(), upReq)
	if err != nil {
		slog.Error("native upstream error", "error", err)
		writeJSONError(w, statusFromError(err), err.Error(), "backend_error")
		return
	}
	defer body.Close()

	var text, reasoning strings.Builder
	var toolCalls []openai.ToolCall
	var usage openai.UsageInfo
	finish := ""

	sse := antigravity.NewSSE(body)
	for {
		payload, err := sse.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if r.Context().Err() != nil {
				writeJSONError(w, statusFromError(err), err.Error(), "backend_error")
				return
			}
			writeJSONError(w, http.StatusBadGateway, err.Error(), "backend_error")
			return
		}

		if upErr := decodeUpstreamError(payload); upErr != nil {
			writeJSONError(w, statusFromError(upErr), upErr.Error(), "backend_error")
			return
		}

		var env antigravity.StreamEnvelope
		if err := json.Unmarshal(payload, &env); err != nil || env.Response == nil {
			continue
		}

		chunks, f, u := convert.ConvertChunk(meta, env.Response)
		for _, c := range chunks {
			delta := c.Choices[0].Delta
			text.WriteString(delta.Content)
			reasoning.WriteString(delta.ReasoningContent)
			toolCalls = append(toolCalls, delta.ToolCalls...)
		}
		if f != "" {
			finish = f
		}
		if u != nil {
			usage = *u
		}
	}

	if finish == "" {
		finish = "stop"
	}

	resp := convert.BuildNonStreamResponse(meta, text.String(), reasoning.String(), toolCalls, finish, usage)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)

	slog.Info("native chat completion finished",
		"model", meta.Model,
		"duration", time.Since(startTime),
		"tool_calls", len(toolCalls),
		"total_tokens", usage.TotalTokens,
	)
}

// sseSink serializes writes from the relay loop and the keep-alive ticker.
type sseSink struct {
	w         http.ResponseWriter
	flusher   http.Flusher
	mu        sync.Mutex
	completed bool
	lastWrite time.Time
}

func newSSESink(w http.ResponseWriter, f http.Flusher) *sseSink {
	return &sseSink{w: w, flusher: f, lastWrite: time.Now()}
}

func (s *sseSink) write(payload string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completed {
		return
	}
	if _, err := io.WriteString(s.w, payload); err != nil {
		return
	}
	s.flusher.Flush()
	s.lastWrite = time.Now()
}

func (s *sseSink) chunk(c openai.ChatCompletionChunk) {
	raw, err := json.Marshal(c)
	if err != nil {
		return
	}
	s.write("data: " + string(raw) + "\n\n")
}

func (s *sseSink) finish(meta convert.ChunkMeta, reason string, usage *openai.UsageInfo) {
	r := reason
	chunk := openai.ChatCompletionChunk{
		ID:      meta.ID,
		Object:  "chat.completion.chunk",
		Created: meta.Created,
		Model:   meta.Model,
		Choices: []openai.ChunkChoice{{
			Index:        0,
			Delta:        openai.ChunkDelta{},
			FinishReason: &r,
		}},
		Usage: usage,
	}
	raw, err := json.Marshal(chunk)
	if err != nil {
		return
	}
	s.write("data: " + string(raw) + "\n\n")
}

func (s *sseSink) inBandError(err error) {
	payload, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": err.Error(),
			"type":    "backend_error",
		},
	})
	s.write("data: " + string(payload) + "\n\n")
}

func (s *sseSink) done() {
	s.write("data: [DONE]\n\n")
	s.mu.Lock()
	s.completed = true
	s.mu.Unlock()
}

func (s *sseSink) startKeepAlive() func() {
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(nativeKeepAliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				s.mu.Lock()
				idle := time.Since(s.lastWrite)
				completed := s.completed
				s.mu.Unlock()
				if completed {
					return
				}
				if idle >= nativeKeepAliveInterval {
					s.write(": keep-alive\n\n")
				}
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

// decodeUpstreamError returns a non-nil error when the payload is an error frame.
func decodeUpstreamError(payload []byte) error {
	var env upstreamErrorEnvelope
	if err := json.Unmarshal(payload, &env); err != nil || env.Error == nil {
		return nil
	}
	msg := env.Error.Message
	if msg == "" {
		msg = "upstream error"
	}
	return &antigravity.APIError{StatusCode: statusFromCode(env.Error.Code), Body: msg}
}

func statusFromCode(code int) int {
	if code >= 400 && code <= 599 {
		return code
	}
	return http.StatusBadGateway
}

func statusFromError(err error) int {
	var apiErr *antigravity.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 {
		return apiErr.StatusCode
	}
	if errors.Is(err, context.Canceled) {
		return http.StatusRequestTimeout
	}
	return http.StatusBadGateway
}

func usageTotal(u *openai.UsageInfo) int {
	if u == nil {
		return 0
	}
	return u.TotalTokens
}

func writeJSONError(w http.ResponseWriter, status int, message, kind string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(openai.ErrorResponse{
		Error: openai.APIError{Message: message, Type: kind},
	})
}

// firstUserText seeds the upstream session id so prompt caching can engage.
func firstUserText(msgs []openai.ChatMessage) string {
	for _, m := range msgs {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			return m.Content
		}
	}
	return ""
}
