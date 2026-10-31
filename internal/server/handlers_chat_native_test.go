package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yaping-pro/agyapi/internal/config"
)

type capturedRequest struct {
	Path   string
	Header http.Header
	Body   map[string]any
}

// fakeUpstream stands in for daily-cloudcode-pa.googleapis.com.
type fakeUpstream struct {
	mu      sync.Mutex
	reqs    []capturedRequest
	chunks  []string
	sseDone bool
}

func (f *fakeUpstream) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)

		f.mu.Lock()
		f.reqs = append(f.reqs, capturedRequest{Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
		chunks := append([]string(nil), f.chunks...)
		f.mu.Unlock()

		switch {
		case strings.Contains(r.URL.Path, "streamGenerateContent"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			for _, c := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", c)
			}
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		case strings.Contains(r.URL.Path, "loadCodeAssist"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"currentTier":             map[string]any{"id": "standard-tier"},
				"cloudaicompanionProject": "aicode-consumers",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (f *fakeUpstream) lastStream() capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.reqs) - 1; i >= 0; i-- {
		if strings.Contains(f.reqs[i].Path, "streamGenerateContent") {
			return f.reqs[i]
		}
	}
	return capturedRequest{}
}

// newNativeConfig points the server at the fake upstream and a valid local token.
func newNativeConfig(t *testing.T, upstreamURL string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "antigravity-oauth-token")
	doc := map[string]any{
		"token": map[string]any{
			"access_token":  "test-access-token",
			"token_type":    "Bearer",
			"refresh_token": "test-refresh-token",
			"expiry":        time.Now().Add(2 * time.Hour).Format(time.RFC3339),
			"expires_in":    3599,
		},
		"auth_method":    "consumer",
		"id_token_email": "user@example.com",
	}
	raw, _ := json.Marshal(doc)
	if err := os.WriteFile(tokenPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	return &config.Config{
		Host:           "127.0.0.1",
		Port:           0,
		Backend:        "native",
		TokenPath:      tokenPath,
		Project:        "aicode-consumers",
		UpstreamURL:    upstreamURL,
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
}

func postChat(t *testing.T, h http.Handler, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func parseSSE(rec *httptest.ResponseRecorder) []map[string]any {
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(payload), &m); err == nil {
			out = append(out, m)
		}
	}
	return out
}

// End-to-end: omp-shaped request -> gateway -> upstream -> OpenAI SSE with a
// properly formed tool call, signature-encoded id and real usage numbers.
func TestNativeStreamingToolCallEndToEnd(t *testing.T) {
	up := &fakeUpstream{chunks: []string{
		`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"thinking","thought":true}]}}],"usageMetadata":{"promptTokenCount":900}}}`,
		`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"Let me check.","thoughtSignature":"SIG-1","functionCall":{"name":"bash","args":{"command":"ls"},"id":"call_42"}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":900,"candidatesTokenCount":40,"totalTokenCount":940},"modelVersion":"gemini-3.8-flash-tiered"}}`,
	}}
	ts := httptest.NewServer(up.handler())
	defer ts.Close()

	cfg := newNativeConfig(t, ts.URL)
	h := NewHandler(cfg)

	rec := postChat(t, h, map[string]any{
		"model":  "gemini-3.8-flash-high",
		"stream": true,
		"messages": []any{
			map[string]any{"role": "system", "content": "you are omp"},
			map[string]any{"role": "user", "content": "list files"},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{
				"name": "bash", "description": "run a command",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{"command": map[string]any{"type": "string"}},
				},
			}},
		},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	events := parseSSE(rec)
	if len(events) == 0 {
		t.Fatalf("no SSE events: %s", rec.Body.String())
	}

	var (
		sawReasoning bool
		sawContent   bool
		sawToolCall  bool
		finishReason string
		usageTotal   float64
	)
	for _, ev := range events {
		choices, _ := ev["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		ch := choices[0].(map[string]any)
		if fr, ok := ch["finish_reason"].(string); ok && fr != "" {
			finishReason = fr
		}
		delta, _ := ch["delta"].(map[string]any)
		if delta == nil {
			continue
		}
		if s, _ := delta["content"].(string); s != "" {
			sawContent = true
		}
		if s, _ := delta["reasoning_content"].(string); s != "" {
			sawReasoning = true
		}
		if tcs, ok := delta["tool_calls"].([]any); ok && len(tcs) > 0 {
			sawToolCall = true
			tc := tcs[0].(map[string]any)
			id, _ := tc["id"].(string)
			if !strings.HasPrefix(id, "call_42") || !strings.Contains(id, "__thought__") {
				t.Fatalf("tool_call id = %q, want call_42 + signature", id)
			}
			fn, _ := tc["function"].(map[string]any)
			if fn["name"] != "bash" {
				t.Fatalf("tool name = %v", fn["name"])
			}
			if args, _ := fn["arguments"].(string); args != `{"command":"ls"}` {
				t.Fatalf("arguments = %q", args)
			}
			if idx, ok := tc["index"].(float64); !ok || idx != 0 {
				t.Fatalf("index = %v, want 0", tc["index"])
			}
		}
		if u, ok := ev["usage"].(map[string]any); ok {
			if v, ok := u["total_tokens"].(float64); ok {
				usageTotal = v
			}
		}
	}

	if !sawReasoning {
		t.Error("reasoning_content never streamed")
	}
	if !sawContent {
		t.Error("content never streamed")
	}
	if !sawToolCall {
		t.Error("tool_calls never streamed")
	}
	if finishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", finishReason)
	}
	if usageTotal != 940 {
		t.Errorf("usage.total_tokens = %v, want 940", usageTotal)
	}

	// Upstream must have received a native envelope, not a compiled text blob.
	upReq := up.lastStream()
	if upReq.Body == nil {
		t.Fatal("upstream never received a stream request")
	}
	if upReq.Body["project"] != "aicode-consumers" {
		t.Errorf("project = %v", upReq.Body["project"])
	}
	if upReq.Body["requestType"] != "agent" {
		t.Errorf("requestType = %v", upReq.Body["requestType"])
	}
	if upReq.Body["model"] != "gemini-3.8-flash-tiered" {
		t.Errorf("upstream model = %v, want gemini-3.8-flash-tiered", upReq.Body["model"])
	}
	if got := upReq.Header.Get("Authorization"); got != "Bearer test-access-token" {
		t.Errorf("Authorization = %q", got)
	}
	if got := upReq.Header.Get("requestType"); got != "agent" {
		t.Errorf("requestType header = %q", got)
	}

	req, _ := upReq.Body["request"].(map[string]any)
	if req == nil {
		t.Fatal("inner request missing")
	}
	tools, _ := req["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("tools were not forwarded natively")
	}
	group := tools[0].(map[string]any)
	decls, _ := group["functionDeclarations"].([]any)
	if len(decls) != 1 {
		t.Fatalf("functionDeclarations = %v", group["functionDeclarations"])
	}
	decl := decls[0].(map[string]any)
	if decl["name"] != "bash" {
		t.Errorf("decl name = %v", decl["name"])
	}
	if _, ok := decl["parametersJsonSchema"]; !ok {
		t.Errorf("parametersJsonSchema missing: %v", decl)
	}
	if _, ok := decl["parameters"]; ok {
		t.Errorf("legacy parameters key should not be sent: %v", decl)
	}
	tc, _ := req["toolConfig"].(map[string]any)
	fcc, _ := tc["functionCallingConfig"].(map[string]any)
	if fcc["mode"] != "VALIDATED" {
		t.Errorf("toolConfig mode = %v, want VALIDATED", fcc["mode"])
	}
}

// Root cause #3 guard at the HTTP boundary: developer reminders ride in the
// conversation history, never in the top-level system instruction.
func TestNativeKeepsDeveloperReminderInHistory(t *testing.T) {
	up := &fakeUpstream{chunks: []string{
		`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":1,"totalTokenCount":101}}}`,
	}}
	ts := httptest.NewServer(up.handler())
	defer ts.Close()

	cfg := newNativeConfig(t, ts.URL)
	h := NewHandler(cfg)

	rec := postChat(t, h, map[string]any{
		"model":  "gemini-3.8-flash-high",
		"stream": true,
		"messages": []any{
			map[string]any{"role": "system", "content": "BASE-SYSTEM"},
			map[string]any{"role": "user", "content": "start"},
			map[string]any{"role": "developer", "content": "<system-reminder>4 incomplete todos</system-reminder>"},
			map[string]any{"role": "user", "content": "直接更新"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	req, _ := up.lastStream().Body["request"].(map[string]any)
	if req == nil {
		t.Fatal("inner request missing")
	}

	sys, _ := req["systemInstruction"].(map[string]any)
	sysText := ""
	if sys != nil {
		parts, _ := sys["parts"].([]any)
		for _, p := range parts {
			if m, ok := p.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					sysText += s
				}
			}
		}
	}
	if sysText == "" {
		t.Log("systemInstruction empty; acceptable only if system prompt was dropped")
	}
	if strings.Contains(sysText, "incomplete todos") {
		t.Fatalf("developer reminder promoted into systemInstruction: %q", sysText)
	}

	contents, _ := req["contents"].([]any)
	var history string
	for _, c := range contents {
		turn, _ := c.(map[string]any)
		parts, _ := turn["parts"].([]any)
		for _, p := range parts {
			if m, ok := p.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					history += s + "\n"
				}
			}
		}
	}
	if !strings.Contains(history, "incomplete todos") {
		t.Fatalf("developer reminder lost from history: %q", history)
	}
	if !strings.Contains(history, "直接更新") {
		t.Fatalf("latest user instruction missing from history: %q", history)
	}
	if i, j := strings.Index(history, "incomplete todos"), strings.Index(history, "直接更新"); i > j {
		t.Fatalf("history order inverted: reminder at %d, latest instruction at %d", i, j)
	}
}

// Non-streaming must return a regular chat.completion object.
func TestNativeNonStreaming(t *testing.T) {
	up := &fakeUpstream{chunks: []string{
		`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"totalTokenCount":12}}}`,
	}}
	ts := httptest.NewServer(up.handler())
	defer ts.Close()

	h := NewHandler(newNativeConfig(t, ts.URL))
	rec := postChat(t, h, map[string]any{
		"model":    "gemini-3.8-flash-high",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rec.Body.String())
	}
	if resp["object"] != "chat.completion" {
		t.Fatalf("object = %v", resp["object"])
	}
	choices, _ := resp["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices = %v", resp["choices"])
	}
	ch := choices[0].(map[string]any)
	msg, _ := ch["message"].(map[string]any)
	if msg["content"] != "done" {
		t.Fatalf("content = %v", msg["content"])
	}
	if ch["finish_reason"] != "stop" {
		t.Fatalf("finish_reason = %v", ch["finish_reason"])
	}
	usage, _ := resp["usage"].(map[string]any)
	if usage["total_tokens"] != float64(12) {
		t.Fatalf("usage = %v", usage)
	}
}

// A failing upstream must surface as a structured error, not a hung stream.
func TestNativeUpstreamErrorSurfaces(t *testing.T) {
	up := &fakeUpstream{chunks: []string{`{"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}}`}}
	ts := httptest.NewServer(up.handler())
	defer ts.Close()

	h := NewHandler(newNativeConfig(t, ts.URL))
	rec := postChat(t, h, map[string]any{
		"model":    "gemini-3.8-flash-high",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})

	if rec.Code == http.StatusOK {
		// Streaming mode reports errors in-band; non-streaming must not 200 an error.
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
			if _, ok := resp["error"]; !ok {
				t.Fatalf("200 with no error object: %s", rec.Body.String())
			}
		}
		return
	}
	if rec.Code != http.StatusTooManyRequests && rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}
