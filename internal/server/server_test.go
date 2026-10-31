package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yaping-pro/agyapi/internal/config"
	"github.com/yaping-pro/agyapi/internal/openai"
)

func writeMockScript(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "mock_agy.sh")
	if err := os.WriteFile(scriptPath, []byte(content), 0755); err != nil {
		t.Fatalf("failed to write mock script: %v", err)
	}
	return scriptPath
}

func TestCORS_OptionsAndHeaders(t *testing.T) {
	cfg := &config.Config{
		Host:           "127.0.0.1",
		Port:           8000,
		AgyPath:        "/bin/echo",
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)

	// 1. OPTIONS preflight
	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected OPTIONS status 200, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected Access-Control-Allow-Origin: *, got '%s'", w.Header().Get("Access-Control-Allow-Origin"))
	}
	if !strings.Contains(w.Header().Get("Access-Control-Allow-Methods"), "OPTIONS") {
		t.Errorf("expected Access-Control-Allow-Methods to contain OPTIONS, got '%s'", w.Header().Get("Access-Control-Allow-Methods"))
	}
	if w.Header().Get("Access-Control-Allow-Headers") != "*" {
		t.Errorf("expected Access-Control-Allow-Headers: *, got '%s'", w.Header().Get("Access-Control-Allow-Headers"))
	}

	// 2. Normal GET /health has CORS headers
	getReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	getW := httptest.NewRecorder()
	s.Handler.ServeHTTP(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Fatalf("expected GET /health status 200, got %d", getW.Code)
	}
	if getW.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected CORS header on GET request, got '%s'", getW.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestLimitConcurrency(t *testing.T) {
	mw := NewMiddleware(2)

	started := make(chan struct{}, 2)
	block := make(chan struct{})

	testHandler := mw.LimitConcurrency(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-block
		w.WriteHeader(http.StatusOK)
	}))

	// Start two requests that hold the semaphore
	for i := 0; i < 2; i++ {
		go func() {
			req := httptest.NewRequest(http.MethodPost, "/test", nil)
			w := httptest.NewRecorder()
			testHandler.ServeHTTP(w, req)
		}()
	}

	<-started
	<-started

	if mw.ActiveRequests() != 2 {
		t.Fatalf("expected 2 active requests, got %d", mw.ActiveRequests())
	}

	// A third request should be blocked
	thirdDone := make(chan struct{})
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		w := httptest.NewRecorder()
		testHandler.ServeHTTP(w, req)
		close(thirdDone)
	}()

	select {
	case <-thirdDone:
		t.Fatalf("third request should have blocked under concurrency limit")
	case <-time.After(50 * time.Millisecond):
		// Expected to block
	}

	// Release blocking requests
	close(block)
	<-thirdDone

	// Give a moment for defer handlers to finish decrementing
	time.Sleep(20 * time.Millisecond)
	if mw.ActiveRequests() != 0 {
		t.Errorf("expected 0 active requests after completion, got %d", mw.ActiveRequests())
	}
}

func TestHealthEndpoint(t *testing.T) {
	cfg := &config.Config{
		AgyPath:        "/bin/echo",
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("expected JSON content type, got '%s'", w.Header().Get("Content-Type"))
	}

	var data map[string]any
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("failed to decode health response: %v", err)
	}

	if data["status"] != "ok" {
		t.Errorf("expected status 'ok', got '%v'", data["status"])
	}
	if data["version"] != "1.0.0" {
		t.Errorf("expected version '1.0.0', got '%v'", data["version"])
	}
	if data["agy_available"] != true {
		t.Errorf("expected agy_available true, got '%v'", data["agy_available"])
	}
	if data["agy_path"] != "echo" {
		t.Errorf("expected agy_path 'echo', got '%v'", data["agy_path"])
	}
	if data["default_model"] != "gemini-3.8-flash-high" {
		t.Errorf("expected default_model 'gemini-3.8-flash-high', got '%v'", data["default_model"])
	}
	if int(data["max_concurrency"].(float64)) != 4 {
		t.Errorf("expected max_concurrency 4, got '%v'", data["max_concurrency"])
	}
	if int(data["active_requests"].(float64)) != 0 {
		t.Errorf("expected active_requests 0, got '%v'", data["active_requests"])
	}
}

func TestModelsEndpoints(t *testing.T) {
	cfg := &config.Config{
		DefaultModel: "gemini-3.8-flash-high",
	}
	s := NewServer(cfg)

	// List models
	listReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	listW := httptest.NewRecorder()
	s.Handler.ServeHTTP(listW, listReq)

	if listW.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", listW.Code)
	}

	var listResp openai.ModelListResponse
	if err := json.NewDecoder(listW.Body).Decode(&listResp); err != nil {
		t.Fatalf("failed to decode model list: %v", err)
	}
	if listResp.Object != "list" {
		t.Errorf("expected object 'list', got '%s'", listResp.Object)
	}
	if len(listResp.Data) == 0 {
		t.Fatalf("expected non-empty model list")
	}

	foundDefault := false
	for _, m := range listResp.Data {
		if m.ID == "gemini-3.8-flash-high" {
			foundDefault = true
		}
		if m.OwnedBy != "antigravity" {
			t.Errorf("expected owned_by 'antigravity', got '%s'", m.OwnedBy)
		}
	}
	if !foundDefault {
		t.Errorf("expected default model to be in list")
	}

	// Single model detail
	detailReq := httptest.NewRequest(http.MethodGet, "/v1/models/gemini-3.8-flash-high", nil)
	detailW := httptest.NewRecorder()
	s.Handler.ServeHTTP(detailW, detailReq)

	if detailW.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", detailW.Code)
	}
	var modelObj openai.ModelObject
	if err := json.NewDecoder(detailW.Body).Decode(&modelObj); err != nil {
		t.Fatalf("failed to decode model object: %v", err)
	}
	if modelObj.ID != "gemini-3.8-flash-high" {
		t.Errorf("expected ID 'gemini-3.8-flash-high', got '%s'", modelObj.ID)
	}
	if modelObj.OwnedBy != "antigravity" {
		t.Errorf("expected OwnedBy 'antigravity', got '%s'", modelObj.OwnedBy)
	}

	// Model detail without ID -> 400 Bad Request
	emptyReq := httptest.NewRequest(http.MethodGet, "/v1/models/", nil)
	emptyW := httptest.NewRecorder()
	s.Handler.ServeHTTP(emptyW, emptyReq)

	if emptyW.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty model ID, got %d", emptyW.Code)
	}

	// Model detail with unknown ID -> 404 Not Found
	notFoundReq := httptest.NewRequest(http.MethodGet, "/v1/models/non-existent-model", nil)
	notFoundW := httptest.NewRecorder()
	s.Handler.ServeHTTP(notFoundW, notFoundReq)

	if notFoundW.Code != http.StatusNotFound {
		t.Errorf("expected status 404 for unknown model ID, got %d", notFoundW.Code)
	}
	var errResp openai.ErrorResponse
	if err := json.NewDecoder(notFoundW.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Error.Code != "model_not_found" {
		t.Errorf("expected error code 'model_not_found', got '%v'", errResp.Error.Code)
	}
	if errResp.Error.Type != "invalid_request_error" {
		t.Errorf("expected error type 'invalid_request_error', got '%s'", errResp.Error.Type)
	}
	if errResp.Error.Param == nil || *errResp.Error.Param != "model" {
		t.Errorf("expected error param 'model', got '%v'", errResp.Error.Param)
	}
}

func TestChatCompletions_InvalidJSON(t *testing.T) {
	cfg := &config.Config{
		DefaultModel: "gemini-3.8-flash-high",
	}
	s := NewServer(cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString("{invalid-json"))
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid_request_error") {
		t.Errorf("expected invalid_request_error in body, got '%s'", w.Body.String())
	}
}

func TestChatCompletions_EmptyMessages(t *testing.T) {
	cfg := &config.Config{
		DefaultModel: "gemini-3.8-flash-high",
	}
	s := NewServer(cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gemini-3.8-flash-high","messages":[]}`))
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	var errResp openai.ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Error.Type != "invalid_request_error" {
		t.Errorf("expected type 'invalid_request_error', got '%s'", errResp.Error.Type)
	}
	if !strings.Contains(errResp.Error.Message, "messages array cannot be empty") {
		t.Errorf("expected message to mention messages array cannot be empty, got '%s'", errResp.Error.Message)
	}
}

func TestChatCompletions_PayloadTooLarge(t *testing.T) {
	cfg := &config.Config{
		DefaultModel: "gemini-3.8-flash-high",
	}
	s := NewServer(cfg)

	// Create a reader exceeding 16MB limit
	largePayload := bytes.Repeat([]byte("a"), (16<<20)+1024)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(largePayload))
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for payload exceeding 16MB limit, got %d", w.Code)
	}
}

func TestChatCompletions_NonStreaming_Normal(t *testing.T) {
	script := `#!/bin/bash
cat > /dev/null
cat << 'EOF'
{
  "conversation_id": "conv-123",
  "status": "SUCCESS",
  "response": "Paris is the capital of France.",
  "duration_seconds": 0.42,
  "usage": {
    "input_tokens": 15,
    "output_tokens": 7,
    "thinking_tokens": 0,
    "total_tokens": 22
  }
}
EOF
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{
		AgyPath:        scriptPath,
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)

	reqBody := openai.ChatCompletionRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "What is the capital of France?"},
		},
		Stream: false,
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp openai.ChatCompletionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop', got '%s'", resp.Choices[0].FinishReason)
	}
	if resp.Choices[0].Message.Content != "Paris is the capital of France." {
		t.Errorf("unexpected content: '%s'", resp.Choices[0].Message.Content)
	}
	if resp.Usage.PromptTokens != 15 || resp.Usage.CompletionTokens != 7 || resp.Usage.TotalTokens != 22 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
	if !strings.HasPrefix(resp.ID, "chatcmpl-") {
		t.Errorf("expected ID starting with 'chatcmpl-', got '%s'", resp.ID)
	}
}

func TestChatCompletions_NonStreaming_ToolCalls(t *testing.T) {
	script := `#!/bin/bash
cat > /dev/null
cat << 'EOF'
{
  "conversation_id": "conv-tool",
  "status": "SUCCESS",
  "response": "Checking the weather now.\n<tool_call>\n{\"name\": \"get_current_weather\", \"arguments\": {\"location\": \"Tokyo\"}}\n</tool_call>",
  "duration_seconds": 0.5,
  "usage": {
    "input_tokens": 20,
    "output_tokens": 12,
    "total_tokens": 32
  }
}
EOF
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{
		AgyPath:        scriptPath,
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)

	reqBody := openai.ChatCompletionRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "What is the weather in Tokyo?"},
		},
		Tools: []openai.ToolDefinition{
			{
				Type: "function",
				Function: openai.ToolFunctionDef{
					Name:        "get_current_weather",
					Description: "Get the current weather",
				},
			},
		},
		Stream: false,
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp openai.ChatCompletionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	choice := resp.Choices[0]
	if choice.FinishReason != "tool_calls" {
		t.Errorf("expected finish_reason 'tool_calls', got '%s'", choice.FinishReason)
	}
	if choice.Message.Content != "Checking the weather now." {
		t.Errorf("unexpected content: '%s'", choice.Message.Content)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(choice.Message.ToolCalls))
	}
	tc := choice.Message.ToolCalls[0]
	if tc.Function.Name != "get_current_weather" {
		t.Errorf("expected tool name 'get_current_weather', got '%s'", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"location": "Tokyo"}` {
		t.Errorf("unexpected arguments: '%s'", tc.Function.Arguments)
	}
}

func TestChatCompletions_ModelFallback(t *testing.T) {
	script := `#!/bin/bash
model_arg=""
while [[ $# -gt 0 ]]; do
  case $1 in
    --model)
      model_arg="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

cat > /dev/null
echo "{\"conversation_id\": \"123\", \"status\": \"SUCCESS\", \"response\": \"Model received: $model_arg\", \"usage\": {\"input_tokens\": 5, \"output_tokens\": 5, \"total_tokens\": 10}}"
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{
		AgyPath:        scriptPath,
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)

	// Case 1: Empty model and generic aliases -> should fallback to DefaultModel
	for _, alias := range []string{"", "default", "gpt-4o", "gpt-4"} {
		reqBody := openai.ChatCompletionRequest{
			Model: alias,
			Messages: []openai.ChatMessage{
				{Role: "user", Content: "hi"},
			},
			Stream: false,
		}
		b, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
		w := httptest.NewRecorder()
		s.Handler.ServeHTTP(w, req)

		var resp openai.ChatCompletionResponse
		_ = json.NewDecoder(w.Body).Decode(&resp)
		if !strings.Contains(resp.Choices[0].Message.Content, "gemini-3.8-flash-high") {
			t.Errorf("expected fallback to default model for alias %q, got: '%s'", alias, resp.Choices[0].Message.Content)
		}
	}

	// Case 2: Custom / unknown model -> should forward directly to agy
	reqBody := openai.ChatCompletionRequest{
		Model: "custom-model-2026",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "hi"},
		},
		Stream: false,
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	var resp2 openai.ChatCompletionResponse
	_ = json.NewDecoder(w.Body).Decode(&resp2)
	if !strings.Contains(resp2.Choices[0].Message.Content, "custom-model-2026") {
		t.Errorf("expected custom/unknown model to be forwarded directly, got: '%s'", resp2.Choices[0].Message.Content)
	}

	// Case 3: Leading hyphen model -> should reject flag injection and fallback to DefaultModel
	for _, flagModel := range []string{"--flag-injection", "-f", "--help"} {
		reqBodyFlag := openai.ChatCompletionRequest{
			Model: flagModel,
			Messages: []openai.ChatMessage{
				{Role: "user", Content: "hi"},
			},
			Stream: false,
		}
		bFlag, _ := json.Marshal(reqBodyFlag)
		reqFlag := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bFlag))
		wFlag := httptest.NewRecorder()
		s.Handler.ServeHTTP(wFlag, reqFlag)

		var respFlag openai.ChatCompletionResponse
		_ = json.NewDecoder(wFlag.Body).Decode(&respFlag)
		if !strings.Contains(respFlag.Choices[0].Message.Content, "gemini-3.8-flash-high") {
			t.Errorf("expected fallback to default model for flag injection model %q, got: '%s'", flagModel, respFlag.Choices[0].Message.Content)
		}
	}
}

func TestChatCompletions_Streaming_Normal(t *testing.T) {
	script := `#!/bin/bash
cat > /dev/null
cat << 'EOF'
{"event":"init","session_id":"sess-123"}
{"event":"step_update","step_update":{"conversation_id":"sess-123","step_index":1,"state":"ACTIVE","text_delta":"Hello "}}
{"event":"step_update","step_update":{"conversation_id":"sess-123","step_index":2,"state":"ACTIVE","text_delta":"world!"}}
{"event":"result","result":{"status":"SUCCESS","response":"Hello world!","usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}}
EOF
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{
		AgyPath:        scriptPath,
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)

	reqBody := openai.ChatCompletionRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "Hello"},
		},
		Stream: true,
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Errorf("expected text/event-stream content type, got '%s'", w.Header().Get("Content-Type"))
	}

	body := w.Body.String()
	lines := strings.Split(body, "\n")

	var dataLines []string
	for _, l := range lines {
		if strings.HasPrefix(l, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(l, "data: "))
		}
	}

	if len(dataLines) < 4 {
		t.Fatalf("expected at least 4 data lines, got %d (full body: %s)", len(dataLines), body)
	}

	// 1. Initial role chunk
	var initChunk openai.ChatCompletionChunk
	if err := json.Unmarshal([]byte(dataLines[0]), &initChunk); err != nil {
		t.Fatalf("failed to unmarshal init chunk: %v", err)
	}
	if initChunk.Choices[0].Delta.Role != "assistant" {
		t.Errorf("expected role assistant in init chunk, got '%s'", initChunk.Choices[0].Delta.Role)
	}

	// 2. Middle chunks: text deltas
	var combinedText strings.Builder
	for i := 1; i < len(dataLines)-2; i++ {
		var chunk openai.ChatCompletionChunk
		if err := json.Unmarshal([]byte(dataLines[i]), &chunk); err == nil {
			combinedText.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	if combinedText.String() != "Hello world!" {
		t.Errorf("expected combined text 'Hello world!', got '%s'", combinedText.String())
	}

	// 3. Final stop chunk
	stopChunkJSON := dataLines[len(dataLines)-2]
	var stopChunk openai.ChatCompletionChunk
	if err := json.Unmarshal([]byte(stopChunkJSON), &stopChunk); err != nil {
		t.Fatalf("failed to unmarshal stop chunk: %v", err)
	}
	if stopChunk.Choices[0].FinishReason == nil || *stopChunk.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop' in final chunk")
	}

	// 4. [DONE]
	if dataLines[len(dataLines)-1] != "[DONE]" {
		t.Errorf("expected final data line to be [DONE], got '%s'", dataLines[len(dataLines)-1])
	}
}

func TestChatCompletions_Streaming_KeepAlive(t *testing.T) {
	// Temporarily set keepAliveInterval to 25ms for fast deterministic testing
	oldInterval := keepAliveInterval
	keepAliveInterval = 25 * time.Millisecond
	defer func() { keepAliveInterval = oldInterval }()

	script := `#!/bin/bash
cat > /dev/null
echo '{"event":"step_update","step_update":{"conversation_id":"123","step_index":1,"state":"ACTIVE","text_delta":"First"}}'
sleep 0.08
echo '{"event":"step_update","step_update":{"conversation_id":"123","step_index":2,"state":"ACTIVE","text_delta":"Second"}}'
echo '{"event":"result","result":{"status":"SUCCESS","response":"FirstSecond","usage":{"input_tokens":5,"output_tokens":2}}}'
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{
		AgyPath:        scriptPath,
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)

	reqBody := openai.ChatCompletionRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "Hi"},
		},
		Stream: true,
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, ": keep-alive\n\n") {
		t.Errorf("expected keep-alive comment in SSE stream, got:\n%s", body)
	}
}

func TestChatCompletions_Streaming_ErrorMidStream(t *testing.T) {
	script := `#!/bin/bash
cat > /dev/null
echo '{"event":"step_update","step_update":{"conversation_id":"123","step_index":1,"state":"ACTIVE","text_delta":"Starting..."}}'
echo "critical process failure" >&2
exit 1
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{
		AgyPath:        scriptPath,
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)

	reqBody := openai.ChatCompletionRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "Hi"},
		},
		Stream: true,
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `"error"`) {
		t.Errorf("expected error payload in SSE body on mid-stream failure, got:\n%s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("expected [DONE] at end of errored stream, got:\n%s", body)
	}
}

func TestChatCompletions_NonStreaming_BackendError(t *testing.T) {
	script := `#!/bin/bash
cat > /dev/null
echo "fatal error in agy" >&2
exit 1
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{
		AgyPath:        scriptPath,
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)

	reqBody := openai.ChatCompletionRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "Hi"},
		},
		Stream: false,
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 on backend error, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "backend_error") {
		t.Errorf("expected backend_error in body, got '%s'", w.Body.String())
	}
}

func TestLimitConcurrency_ContextCancellation(t *testing.T) {
	mw := NewMiddleware(1)

	started := make(chan struct{})
	block := make(chan struct{})

	testHandler := mw.LimitConcurrency(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-block
		w.WriteHeader(http.StatusOK)
	}))

	// Start 1 request holding semaphore of capacity 1
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		w := httptest.NewRecorder()
		testHandler.ServeHTTP(w, req)
	}()

	<-started

	// Second request with cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	req2 := httptest.NewRequest(http.MethodPost, "/test", nil).WithContext(ctx)
	w2 := httptest.NewRecorder()

	cancelDone := make(chan struct{})
	go func() {
		testHandler.ServeHTTP(w2, req2)
		close(cancelDone)
	}()

	// Cancel context of req2
	cancel()

	select {
	case <-cancelDone:
		// Success, unblocked due to context cancellation
	case <-time.After(1 * time.Second):
		t.Fatalf("req2 did not unblock on context cancellation")
	}

	// Active requests should still be 1 (from first request)
	if mw.ActiveRequests() != 1 {
		t.Errorf("expected 1 active request, got %d", mw.ActiveRequests())
	}

	// Release first request
	close(block)
	time.Sleep(20 * time.Millisecond)

	if mw.ActiveRequests() != 0 {
		t.Errorf("expected 0 active requests after completion, got %d", mw.ActiveRequests())
	}
}

func TestChatCompletions_Streaming_NoErrorAfterCompletion(t *testing.T) {
	script := `#!/bin/bash
cat > /dev/null
echo '{"event":"step_update","step_update":{"conversation_id":"123","step_index":1,"state":"ACTIVE","text_delta":"Successful response"}}'
echo '{"event":"result","result":{"status":"SUCCESS","response":"Successful response","usage":{"input_tokens":5,"output_tokens":2}}}'
echo "trailing error on exit" >&2
exit 1
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{
		AgyPath:        scriptPath,
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)

	reqBody := openai.ChatCompletionRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "Hi"},
		},
		Stream: true,
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, `"error"`) {
		t.Errorf("expected no error payload after successful completion, got:\n%s", body)
	}
	doneCount := strings.Count(body, "data: [DONE]")
	if doneCount != 1 {
		t.Errorf("expected exactly 1 data: [DONE], got %d in:\n%s", doneCount, body)
	}
}

func TestChatCompletions_Streaming_FallbackOnCleanExitWithoutResult(t *testing.T) {
	script := `#!/bin/bash
cat > /dev/null
echo '{"event":"step_update","step_update":{"conversation_id":"123","step_index":1,"state":"ACTIVE","text_delta":"Output without result event"}}'
exit 0
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{
		AgyPath:        scriptPath,
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)

	reqBody := openai.ChatCompletionRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "Hi"},
		},
		Stream: true,
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "Output without result event") {
		t.Errorf("expected delta in body, got:\n%s", body)
	}
	if !strings.Contains(body, `"finish_reason":"stop"`) {
		t.Errorf("expected finish_reason stop in fallback stop chunk, got:\n%s", body)
	}
	doneCount := strings.Count(body, "data: [DONE]")
	if doneCount != 1 {
		t.Errorf("expected exactly 1 data: [DONE], got %d in:\n%s", doneCount, body)
	}
}

type failingResponseWriter struct {
	header     http.Header
	statusCode int
	writeCount int
	failAfter  int
}

func (f *failingResponseWriter) Header() http.Header {
	return f.header
}

func (f *failingResponseWriter) Write(p []byte) (int, error) {
	f.writeCount++
	if f.writeCount > f.failAfter {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func (f *failingResponseWriter) WriteHeader(statusCode int) {
	f.statusCode = statusCode
}

func (f *failingResponseWriter) Flush() {}

func TestChatCompletions_Streaming_WriteErrorTerminatesProcess(t *testing.T) {
	script := `#!/bin/bash
cat > /dev/null
while true; do
  echo '{"event":"step_update","step_update":{"conversation_id":"123","step_index":1,"state":"ACTIVE","text_delta":"chunk"}}'
  sleep 0.05
done
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{
		AgyPath:        scriptPath,
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)

	reqBody := openai.ChatCompletionRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "Hi"},
		},
		Stream: true,
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	// fail after initial chunk (write 1 succeeds, write 2 on delta fails)
	fw := &failingResponseWriter{
		header:    make(http.Header),
		failAfter: 1,
	}

	done := make(chan struct{})
	go func() {
		s.Handler.ServeHTTP(fw, req)
		close(done)
	}()

	select {
	case <-done:
		// Succeeded in terminating process quickly upon write error
	case <-time.After(3 * time.Second):
		t.Fatalf("stream handler did not terminate upon client write error")
	}
}

func TestChatCompletions_NonStreaming_ErrorStatus(t *testing.T) {
	script := `#!/bin/bash
cat > /dev/null
echo '{"conversation_id":"err-1","status":"ERROR","response":"Quota exhausted","duration_seconds":0.1,"usage":{"input_tokens":5,"output_tokens":0,"total_tokens":5}}'
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{
		AgyPath:        scriptPath,
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)

	reqBody := openai.ChatCompletionRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "Hello"},
		},
		Stream: false,
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Quota exhausted") {
		t.Errorf("expected response to contain 'Quota exhausted', got: %s", w.Body.String())
	}
}

func TestChatCompletions_Streaming_ErrorStatus(t *testing.T) {
	script := `#!/bin/bash
cat > /dev/null
echo '{"event":"init","session_id":"s-1"}'
echo '{"event":"step_update","step_update":{"state":"ACTIVE","text_delta":"Starting..."}}'
echo '{"event":"result","result":{"status":"ERROR","response":"Stream failed unexpectedly","usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}'
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{
		AgyPath:        scriptPath,
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)

	reqBody := openai.ChatCompletionRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "Hello"},
		},
		Stream: true,
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Stream failed unexpectedly") {
		t.Errorf("expected SSE body to contain error message, got: %s", body)
	}
	if !strings.Contains(body, "backend_error") {
		t.Errorf("expected SSE body to contain backend_error, got: %s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("expected SSE body to contain [DONE], got: %s", body)
	}
}
