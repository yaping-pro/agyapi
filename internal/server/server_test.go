package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yaping-pro/agyapi/internal/config"
	"github.com/yaping-pro/agyapi/internal/openai"
)

func TestCORS_OptionsAndHeaders(t *testing.T) {
	cfg := &config.Config{
		Host:           "127.0.0.1",
		Port:           8000,
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
		TokenPath:      "/dev/null",
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
	if v, _ := data["version"].(string); v == "" {
		t.Errorf("expected non-empty version, got %v", data["version"])
	}
	if data["token_ready"] != true {
		t.Errorf("expected token_ready true, got '%v'", data["token_ready"])
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
