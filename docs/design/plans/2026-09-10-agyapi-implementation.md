# agyapi Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a high-performance, single-binary Go local service (`agyapi`) that provides an OpenAI-compatible API bridge to the local Antigravity CLI (`agy`), allowing Hermes Agent and external clients to use an active Antigravity subscription.

**Architecture:** Pure Go HTTP server (`net/http`) implementing `/v1/chat/completions`, `/v1/models`, and `/health`. Translates chat messages into structured prompts piped to `agy` via `stdin` (eliminating `E2BIG` argument limits), streams responses via SSE (`text/event-stream`) with `http.Flusher`, manages subprocesses in isolated process groups (`Setpgid: true`) to avoid zombie processes, drains stderr asynchronously to prevent deadlocks, and supports Hermes `<tool_call>` extraction.

**Tech Stack:** Go 1.24+ (Standard Library `net/http`, `os/exec`, `bufio`, `encoding/json`, `log/slog`, `syscall`), `go test`.

## Global Constraints
- Standalone binary with zero external Go module dependencies (standard library only).
- All communication with `agy` must pipe prompts over `stdin`, never through `-p` arguments.
- Process execution must configure `Setpgid: true` and kill process groups on cancellation.
- Stderr must be asynchronously drained into a capped buffer to prevent pipe buffer deadlocks.
- Standard OpenAI API contracts: `finish_reason: "tool_calls"` when tools are called, `created` timestamp in model objects, CORS support for web clients.

---

### Task 1: Project Scaffolding, Configuration & OpenAI Data Types

**Files:**
- Create: `go.mod`
- Create: `internal/config/config.go`
- Create: `internal/openai/types.go`
- Test: `internal/openai/types_test.go`

**Interfaces:**
- Consumes: Standard Go standard library.
- Produces:
  - `Config`: Configuration struct with CLI flags/env defaults.
  - `ChatCompletionRequest`: Request struct supporting both string and multi-part content.
  - `ChatCompletionResponse`: OpenAI completion response struct.
  - `ChatCompletionChunk`: OpenAI SSE chunk struct.
  - `ModelListResponse`, `ModelObject`: Model listing structs.

- [ ] **Step 1: Write failing test for flexible content parsing and OpenAI types**

```go
// internal/openai/types_test.go
package openai

import (
	"encoding/json"
	"testing"
)

func TestParseMessageContent_String(t *testing.T) {
	raw := `{"role": "user", "content": "hello world"}`
	var msg ChatMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("failed to unmarshal string content: %v", err)
	}
	if msg.Content != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", msg.Content)
	}
}

func TestParseMessageContent_ArrayOfParts(t *testing.T) {
	raw := `{"role": "user", "content": [{"type": "text", "text": "part1"}, {"type": "text", "text": " part2"}]}`
	var msg ChatMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("failed to unmarshal array content: %v", err)
	}
	if msg.Content != "part1 part2" {
		t.Errorf("expected 'part1 part2', got '%s'", msg.Content)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/openai -v`
Expected: FAIL (package does not exist)

- [ ] **Step 3: Implement go.mod, config.go, and types.go**

```go
// go.mod
module agyapi

go 1.24
```

```go
// internal/config/config.go
package config

import (
	"flag"
	"os"
	"strconv"
)

type Config struct {
	Port           int
	Host           string
	AgyPath        string
	DefaultModel   string
	MaxConcurrency int
	Verbose        bool
}

func Load() *Config {
	cfg := &Config{
		Port:           8000,
		Host:           "127.0.0.1",
		AgyPath:        "~/.local/bin/agy",
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
		Verbose:        false,
	}

	if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			cfg.Port = p
		}
	}
	if envHost := os.Getenv("HOST"); envHost != "" {
		cfg.Host = envHost
	}
	if envAgy := os.Getenv("AGY_PATH"); envAgy != "" {
		cfg.AgyPath = envAgy
	}
	if envModel := os.Getenv("DEFAULT_MODEL"); envModel != "" {
		cfg.DefaultModel = envModel
	}
	if envConc := os.Getenv("MAX_CONCURRENCY"); envConc != "" {
		if c, err := strconv.Atoi(envConc); err == nil {
			cfg.MaxConcurrency = c
		}
	}
	if os.Getenv("VERBOSE") == "true" || os.Getenv("VERBOSE") == "1" {
		cfg.Verbose = true
	}

	flag.IntVar(&cfg.Port, "port", cfg.Port, "Port to listen on")
	flag.StringVar(&cfg.Host, "host", cfg.Host, "Host to bind to")
	flag.StringVar(&cfg.AgyPath, "agy-path", cfg.AgyPath, "Path to agy binary")
	flag.StringVar(&cfg.DefaultModel, "default-model", cfg.DefaultModel, "Default model fallback")
	flag.IntVar(&cfg.MaxConcurrency, "max-concurrency", cfg.MaxConcurrency, "Max concurrent requests")
	flag.BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "Enable verbose logging")

	return cfg
}
```

```go
// internal/openai/types.go
package openai

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

func (m *ChatMessage) UnmarshalJSON(data []byte) error {
	type Alias ChatMessage
	aux := struct {
		RawContent any `json:"content"`
		*Alias
	}{
		Alias: (*Alias)(m),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	switch v := aux.RawContent.(type) {
	case string:
		m.Content = v
	case []any:
		var sb strings.Builder
		for _, part := range v {
			if partMap, ok := part.(map[string]any); ok {
				if txt, exists := partMap["text"].(string); exists {
					sb.WriteString(txt)
				}
			}
		}
		m.Content = sb.String()
	case nil:
		m.Content = ""
	default:
		m.Content = fmt.Sprintf("%v", v)
	}
	return nil
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolDefinition struct {
	Type     string         `json:"type"`
	Function ToolFunctionDef `json:"function"`
}

type ToolFunctionDef struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type ChatCompletionRequest struct {
	Model       string           `json:"model"`
	Messages    []ChatMessage    `json:"messages"`
	Stream      bool             `json:"stream"`
	Temperature *float64         `json:"temperature,omitempty"`
	MaxTokens   *int             `json:"max_tokens,omitempty"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
}

type ChatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   UsageInfo    `json:"usage"`
}

type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatCompletionChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []ChunkChoice     `json:"choices"`
}

type ChunkChoice struct {
	Index        int         `json:"index"`
	Delta        ChunkDelta  `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type ChunkDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ModelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type ModelListResponse struct {
	Object string        `json:"object"`
	Data   []ModelObject `json:"data"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/openai -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go.mod internal/config internal/openai
git commit -m "feat: add config loader and openai request/response types"
```

---

### Task 2: Prompt Compiler & Hermes Tool Call Parser

**Files:**
- Create: `internal/prompt/compiler.go`
- Create: `internal/prompt/hermes.go`
- Test: `internal/prompt/compiler_test.go`
- Test: `internal/prompt/hermes_test.go`

**Interfaces:**
- Consumes: `openai.ChatMessage`, `openai.ToolDefinition`, `openai.ToolCall`.
- Produces:
  - `CompilePrompt(messages []openai.ChatMessage, tools []openai.ToolDefinition) string`: Formats messages and tool schema into an Antigravity prompt.
  - `ExtractHermesToolCalls(content string) (string, []openai.ToolCall)`: Detects and parses `<tool_call>` XML tags into structured `ToolCall` items and strips them from user-visible text.

- [ ] **Step 1: Write failing test for Prompt Compiler and Hermes Tool extraction**

```go
// internal/prompt/hermes_test.go
package prompt

import (
	"testing"
)

func TestExtractHermesToolCalls(t *testing.T) {
	raw := `I will check the weather for you.
<tool_call>
{"name": "get_weather", "arguments": {"location": "Paris, France"}}
</tool_call>`

	cleaned, toolCalls := ExtractHermesToolCalls(raw)
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Function.Name != "get_weather" {
		t.Errorf("expected 'get_weather', got '%s'", toolCalls[0].Function.Name)
	}
	if toolCalls[0].Function.Arguments != `{"location": "Paris, France"}` {
		t.Errorf("unexpected arguments: %s", toolCalls[0].Function.Arguments)
	}
	if cleaned != "I will check the weather for you." {
		t.Errorf("unexpected cleaned content: '%s'", cleaned)
	}
}

func TestExtractHermesToolCalls_NoTools(t *testing.T) {
	raw := "Just a normal answer."
	cleaned, toolCalls := ExtractHermesToolCalls(raw)
	if len(toolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(toolCalls))
	}
	if cleaned != raw {
		t.Errorf("expected '%s', got '%s'", raw, cleaned)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/prompt -v`
Expected: FAIL (package does not exist)

- [ ] **Step 3: Implement compiler.go and hermes.go**

```go
// internal/prompt/hermes.go
package prompt

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"agyapi/internal/openai"
)

var toolCallRegex = regexp.MustCompile(`(?s)<tool_call>\s*(.*?)\s*</tool_call>`)

func generateCallID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "call_" + hex.EncodeToString(b)
}

func ExtractHermesToolCalls(content string) (string, []openai.ToolCall) {
	var toolCalls []openai.ToolCall
	matches := toolCallRegex.FindAllStringSubmatch(content, -1)

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		rawJSON := strings.TrimSpace(match[1])
		var parsed struct {
			Name      string `json:"name"`
			Arguments any    `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
			continue
		}

		var argsStr string
		switch v := parsed.Arguments.(type) {
		case string:
			argsStr = v
		default:
			b, _ := json.Marshal(v)
			argsStr = string(b)
		}

		toolCalls = append(toolCalls, openai.ToolCall{
			ID:   generateCallID(),
			Type: "function",
			Function: openai.FunctionCall{
				Name:      parsed.Name,
				Arguments: argsStr,
			},
		})
	}

	cleaned := toolCallRegex.ReplaceAllString(content, "")
	cleaned = strings.TrimSpace(cleaned)

	return cleaned, toolCalls
}
```

```go
// internal/prompt/compiler.go
package prompt

import (
	"encoding/json"
	"fmt"
	"strings"

	"agyapi/internal/openai"
)

func CompilePrompt(messages []openai.ChatMessage, tools []openai.ToolDefinition) string {
	var sb strings.Builder

	// 1. Compile System / Developer Instructions
	var systemInstructions []string
	for _, msg := range messages {
		if msg.Role == "system" || msg.Role == "developer" {
			if strings.TrimSpace(msg.Content) != "" {
				systemInstructions = append(systemInstructions, msg.Content)
			}
		}
	}

	if len(systemInstructions) > 0 {
		sb.WriteString("[System Instructions]\n")
		sb.WriteString(strings.Join(systemInstructions, "\n\n"))
		sb.WriteString("\n\n")
	}

	// 2. Compile Tools if provided
	if len(tools) > 0 {
		sb.WriteString("[Available Tools]\n")
		toolsJSON, err := json.MarshalIndent(tools, "", "  ")
		if err == nil {
			sb.WriteString(string(toolsJSON))
		}
		sb.WriteString("\n\n")
		sb.WriteString("To call one or more tools, format your response strictly as:\n")
		sb.WriteString("<tool_call>\n{\"name\": \"<function_name>\", \"arguments\": <arguments_object>}\n</tool_call>\n\n")
	}

	// 3. Compile Dialogue History
	sb.WriteString("[Conversation History]\n")
	for _, msg := range messages {
		switch msg.Role {
		case "system", "developer":
			// already placed in header
			continue
		case "user":
			sb.WriteString(fmt.Sprintf("User: %s\n\n", strings.TrimSpace(msg.Content)))
		case "assistant":
			var text = strings.TrimSpace(msg.Content)
			if len(msg.ToolCalls) > 0 {
				var callsText []string
				for _, tc := range msg.ToolCalls {
					callsText = append(callsText, fmt.Sprintf("<tool_call>\n{\"name\": %q, \"arguments\": %s}\n</tool_call>", tc.Function.Name, tc.Function.Arguments))
				}
				if text != "" {
					text = text + "\n" + strings.Join(callsText, "\n")
				} else {
					text = strings.Join(callsText, "\n")
				}
			}
			sb.WriteString(fmt.Sprintf("Assistant: %s\n\n", text))
		case "tool":
			name := msg.Name
			if name == "" {
				name = "tool"
			}
			idStr := ""
			if msg.ToolCallID != "" {
				idStr = fmt.Sprintf(" (id=%s)", msg.ToolCallID)
			}
			sb.WriteString(fmt.Sprintf("Tool Response%s: %s\n\n", idStr, strings.TrimSpace(msg.Content)))
		default:
			sb.WriteString(fmt.Sprintf("%s: %s\n\n", strings.Title(msg.Role), strings.TrimSpace(msg.Content)))
		}
	}

	sb.WriteString("Assistant: ")
	return sb.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/prompt -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/prompt
git commit -m "feat: implement prompt compiler and hermes tool call extractor"
```

---

### Task 3: Subprocess Process Bridge & Output Parsers

**Files:**
- Create: `internal/bridge/types.go`
- Create: `internal/bridge/runner.go`
- Create: `internal/bridge/parser.go`
- Test: `internal/bridge/parser_test.go`

**Interfaces:**
- Consumes: `Config`, `prompt` string, `model` string, `stream` bool, `context.Context`.
- Produces:
  - `AgyResult`: Non-streaming result (`Response`, `InputTokens`, `OutputTokens`, `Duration`).
  - `RunAgyNonStream(ctx, cfg, prompt, model)`: Executes `agy` over `stdin`, drains `stderr`, parses JSON result.
  - `RunAgyStream(ctx, cfg, prompt, model, onDelta, onDone)`: Executes `agy --output-format stream-json`, processes NDJSON events, invokes `onDelta` for text chunks, handles process group signal termination.

- [ ] **Step 1: Write failing test for NDJSON Stream Parser**

```go
// internal/bridge/parser_test.go
package bridge

import (
	"strings"
	"testing"
)

func TestParseStreamLine_TextDelta(t *testing.T) {
	line := `{"event":"step_update","step_update":{"conversation_id":"123","step_index":3,"state":"ACTIVE","text_delta":"Hello world"}`
	delta, done, err := ParseStreamEvent([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done {
		t.Errorf("expected done=false")
	}
	if delta != "Hello world" {
		t.Errorf("expected 'Hello world', got '%s'", delta)
	}
}

func TestParseStreamLine_Result(t *testing.T) {
	line := `{"event":"result","result":{"conversation_id":"123","status":"SUCCESS","response":"Hello world\n","usage":{"input_tokens":100,"output_tokens":20}}}`
	_, done, err := ParseStreamEvent([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !done {
		t.Errorf("expected done=true")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bridge -v`
Expected: FAIL (package does not exist)

- [ ] **Step 3: Implement bridge types, parser, and runner**

```go
// internal/bridge/types.go
package bridge

type AgyJSONResponse struct {
	ConversationID  string   `json:"conversation_id"`
	Status          string   `json:"status"`
	Response        string   `json:"response"`
	DurationSeconds float64  `json:"duration_seconds"`
	Usage           AgyUsage `json:"usage"`
}

type AgyUsage struct {
	InputTokens    int `json:"input_tokens"`
	OutputTokens   int `json:"output_tokens"`
	ThinkingTokens int `json:"thinking_tokens"`
	TotalTokens    int `json:"total_tokens"`
}

type StreamEvent struct {
	Event      string          `json:"event"`
	StepUpdate *StepUpdateInfo `json:"step_update,omitempty"`
	Result     *ResultInfo     `json:"result,omitempty"`
}

type StepUpdateInfo struct {
	State     string `json:"state"`
	TextDelta string `json:"text_delta"`
}

type ResultInfo struct {
	Status   string   `json:"status"`
	Response string   `json:"response"`
	Usage    AgyUsage `json:"usage"`
}
```

```go
// internal/bridge/parser.go
package bridge

import (
	"encoding/json"
)

func ParseStreamEvent(line []byte) (delta string, done bool, err error) {
	var ev StreamEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return "", false, err
	}

	if ev.Event == "step_update" && ev.StepUpdate != nil {
		return ev.StepUpdate.TextDelta, false, nil
	}

	if ev.Event == "result" {
		return "", true, nil
	}

	return "", false, nil
}
```

```go
// internal/bridge/runner.go
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"agyapi/internal/config"
)

type CappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func NewCappedBuffer(max int) *CappedBuffer {
	return &CappedBuffer{max: max}
}

func (c *CappedBuffer) Write(p []byte) (n int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.buf.Len() >= c.max {
		return len(p), nil
	}
	remaining := c.max - c.buf.Len()
	if len(p) > remaining {
		return c.buf.Write(p[:remaining])
	}
	return c.buf.Write(p)
}

func (c *CappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func RunAgyNonStream(ctx context.Context, cfg *config.Config, prompt string, model string) (*AgyJSONResponse, error) {
	args := []string{"--output-format", "json", "--disable-slash-commands"}
	if model != "" {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(ctx, cfg.AgyPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = 3 * time.Second

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open stdout pipe: %w", err)
	}

	stderrBuf := NewCappedBuffer(128 * 1024)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start agy: %w", err)
	}

	go func() {
		defer stdinPipe.Close()
		io.WriteString(stdinPipe, prompt)
	}()

	var stdoutBuf bytes.Buffer
	_, copyErr := io.Copy(&stdoutBuf, stdoutPipe)
	waitErr := cmd.Wait()

	if waitErr != nil {
		return nil, fmt.Errorf("agy failed: %v, stderr: %s", waitErr, stderrBuf.String())
	}
	if copyErr != nil {
		return nil, fmt.Errorf("failed to read agy stdout: %w", copyErr)
	}

	var resp AgyJSONResponse
	if err := json.Unmarshal(stdoutBuf.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse agy json output: %w (output: %s)", err, stdoutBuf.String())
	}

	return &resp, nil
}

func RunAgyStream(ctx context.Context, cfg *config.Config, prompt string, model string, onDelta func(string) error, onDone func(*ResultInfo) error) error {
	args := []string{"--output-format", "stream-json", "--disable-slash-commands"}
	if model != "" {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(ctx, cfg.AgyPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = 3 * time.Second

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to open stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to open stdout pipe: %w", err)
	}

	stderrBuf := NewCappedBuffer(128 * 1024)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start agy: %w", err)
	}

	go func() {
		defer stdinPipe.Close()
		io.WriteString(stdinPipe, prompt)
	}()

	reader := bufio.NewReader(stdoutPipe)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var ev StreamEvent
			if jsonErr := json.Unmarshal(line, &ev); jsonErr == nil {
				if ev.Event == "step_update" && ev.StepUpdate != nil && ev.StepUpdate.TextDelta != "" {
					if err := onDelta(ev.StepUpdate.TextDelta); err != nil {
						break
					}
				} else if ev.Event == "result" && ev.Result != nil {
					if onDone != nil {
						_ = onDone(ev.Result)
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("error reading agy stream: %w", err)
		}
	}

	if waitErr := cmd.Wait(); waitErr != nil {
		// If context was cancelled, return nil or context error
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("agy process ended with error: %v, stderr: %s", waitErr, stderrBuf.String())
	}

	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/bridge -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge
git commit -m "feat: implement process bridge with stdin piping, pgid signaling, and stream parser"
```

---

### Task 4: HTTP Server, Handlers, CORS & Concurrency Control

**Files:**
- Create: `internal/server/middleware.go`
- Create: `internal/server/handlers_health.go`
- Create: `internal/server/handlers_models.go`
- Create: `internal/server/handlers_chat.go`
- Create: `internal/server/server.go`
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: `Config`, `prompt.CompilePrompt`, `prompt.ExtractHermesToolCalls`, `bridge.RunAgyNonStream`, `bridge.RunAgyStream`.
- Produces: `NewServer(cfg *config.Config) *http.Server`.

- [ ] **Step 1: Write failing test for HTTP Server endpoints (Health, Models, Chat mock)**

```go
// internal/server/server_test.go
package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"agyapi/internal/config"
)

func TestHealthEndpoint(t *testing.T) {
	cfg := &config.Config{
		AgyPath:        "/bin/echo",
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
	}
	s := NewServer(cfg)
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestModelsEndpoint(t *testing.T) {
	cfg := &config.Config{
		DefaultModel: "gemini-3.8-flash-high",
	}
	s := NewServer(cfg)
	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server -v`
Expected: FAIL (package does not exist)

- [ ] **Step 3: Implement middleware and handlers**

```go
// internal/server/middleware.go
package server

import (
	"net/http"
	"sync/atomic"
)

type Middleware struct {
	sem            chan struct{}
	activeRequests int64
}

func NewMiddleware(maxConcurrency int) *Middleware {
	return &Middleware{
		sem: make(chan struct{}, maxConcurrency),
	}
}

func (m *Middleware) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) LimitConcurrency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.sem <- struct{}{}
		atomic.AddInt64(&m.activeRequests, 1)
		defer func() {
			<-m.sem
			atomic.AddInt64(&m.activeRequests, -1)
		}()

		next.ServeHTTP(w, r)
	})
}
```

```go
// internal/server/handlers_health.go
package server

import (
	"encoding/json"
	"net/http"
	"sync/atomic"

	"agyapi/internal/config"
)

func handleHealth(cfg *config.Config, mw *Middleware) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		active := atomic.LoadInt64(&mw.activeRequests)
		resp := map[string]any{
			"status":          "ok",
			"version":         "1.0.0",
			"agy_path":        cfg.AgyPath,
			"default_model":   cfg.DefaultModel,
			"active_requests": active,
			"max_concurrency": cfg.MaxConcurrency,
		}
		json.NewEncoder(w).Encode(resp)
	}
}
```

```go
// internal/server/handlers_models.go
package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"agyapi/internal/config"
	"agyapi/internal/openai"
)

var knownModels = []string{
	"gemini-3.8-flash-high",
	"gemini-3.8-flash-medium",
	"gemini-3.8-flash-low",
	"gemini-3.7-flash-high",
	"gemini-3.7-flash-medium",
	"gemini-3.7-flash-low",
	"gemini-3.1-pro-high",
	"gemini-3.1-pro-low",
	"claude-sonnet-4-6",
	"claude-opus-4-6-thinking",
	"gpt-oss-120b-medium",
}

func handleModels(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var models []openai.ModelObject
		for _, m := range knownModels {
			models = append(models, openai.ModelObject{
				ID:      m,
				Object:  "model",
				Created: 1725980000,
				OwnedBy: "antigravity",
			})
		}
		resp := openai.ModelListResponse{
			Object: "list",
			Data:   models,
		}
		json.NewEncoder(w).Encode(resp)
	}
}

func handleModelDetail(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		modelID := strings.TrimPrefix(r.URL.Path, "/v1/models/")
		if modelID == "" {
			http.Error(w, `{"error":{"message":"model id required"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openai.ModelObject{
			ID:      modelID,
			Object:  "model",
			Created: 1725980000,
			OwnedBy: "antigravity",
		})
	}
}
```

```go
// internal/server/handlers_chat.go
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"agyapi/internal/bridge"
	"agyapi/internal/config"
	"agyapi/internal/openai"
	"agyapi/internal/prompt"
)

func generateChatID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "chatcmpl-" + hex.EncodeToString(b)
}

func handleChatCompletions(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req openai.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":{"message":"invalid JSON payload: %v","type":"invalid_request_error"}}`, err), http.StatusBadRequest)
			return
		}

		model := req.Model
		if model == "" || model == "default" || model == "gpt-4o" {
			model = cfg.DefaultModel
		}

		compiledPrompt := prompt.CompilePrompt(req.Messages, req.Tools)
		chatId := generateChatID()
		createdTime := time.Now().Unix()

		if req.Stream {
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, `{"error":{"message":"Streaming unsupported"}}`, http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.WriteHeader(http.StatusOK)
			flusher.Flush()

			// Send initial role chunk
			initialChunk := openai.ChatCompletionChunk{
				ID:      chatId,
				Object:  "chat.completion.chunk",
				Created: createdTime,
				Model:   model,
				Choices: []openai.ChunkChoice{
					{
						Index: 0,
						Delta: openai.ChunkDelta{Role: "assistant", Content: ""},
					},
				},
			}
			initBytes, _ := json.Marshal(initialChunk)
			fmt.Fprintf(w, "data: %s\n\n", initBytes)
			flusher.Flush()

			onDelta := func(delta string) error {
				chunk := openai.ChatCompletionChunk{
					ID:      chatId,
					Object:  "chat.completion.chunk",
					Created: createdTime,
					Model:   model,
					Choices: []openai.ChunkChoice{
						{
							Index: 0,
							Delta: openai.ChunkDelta{Content: delta},
						},
					},
				}
				b, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\n", b)
				flusher.Flush()
				return nil
			}

			onDone := func(res *bridge.ResultInfo) error {
				stop := "stop"
				finalChunk := openai.ChatCompletionChunk{
					ID:      chatId,
					Object:  "chat.completion.chunk",
					Created: createdTime,
					Model:   model,
					Choices: []openai.ChunkChoice{
						{
							Index:        0,
							Delta:        openai.ChunkDelta{},
							FinishReason: &stop,
						},
					},
				}
				b, _ := json.Marshal(finalChunk)
				fmt.Fprintf(w, "data: %s\n\n", b)
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return nil
			}

			err := bridge.RunAgyStream(r.Context(), cfg, compiledPrompt, model, onDelta, onDone)
			if err != nil && r.Context().Err() == nil {
				slog.Error("error during agy stream", "error", err)
				fmt.Fprintf(w, "data: {\"error\":{\"message\":%q,\"type\":\"backend_error\"}}\n\ndata: [DONE]\n\n", err.Error())
				flusher.Flush()
			}
			return
		}

		// Non-streaming execution
		res, err := bridge.RunAgyNonStream(r.Context(), cfg, compiledPrompt, model)
		if err != nil {
			slog.Error("agy non-stream error", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": err.Error(),
					"type":    "backend_error",
				},
			})
			return
		}

		cleanedContent, toolCalls := prompt.ExtractHermesToolCalls(res.Response)
		finishReason := "stop"
		if len(toolCalls) > 0 {
			finishReason = "tool_calls"
		}

		resp := openai.ChatCompletionResponse{
			ID:      chatId,
			Object:  "chat.completion",
			Created: createdTime,
			Model:   model,
			Choices: []openai.ChatChoice{
				{
					Index: 0,
					Message: openai.ChatMessage{
						Role:      "assistant",
						Content:   cleanedContent,
						ToolCalls: toolCalls,
					},
					FinishReason: finishReason,
				},
			},
			Usage: openai.UsageInfo{
				PromptTokens:     res.Usage.InputTokens,
				CompletionTokens: res.Usage.OutputTokens,
				TotalTokens:      res.Usage.TotalTokens,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
```

```go
// internal/server/server.go
package server

import (
	"fmt"
	"net/http"

	"agyapi/internal/config"
)

func NewServer(cfg *config.Config) *http.Server {
	mux := http.NewServeMux()
	mw := NewMiddleware(cfg.MaxConcurrency)

	mux.HandleFunc("GET /health", handleHealth(cfg, mw))
	mux.HandleFunc("GET /v1/models", handleModels(cfg))
	mux.HandleFunc("GET /v1/models/", handleModelDetail(cfg))
	mux.HandleFunc("POST /v1/chat/completions", mw.LimitConcurrency(handleChatCompletions(cfg)).ServeHTTP)

	handler := mw.CORS(mux)

	return &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:      handler,
		WriteTimeout: 0, // SSE streams can run as long as required
		ReadTimeout:  30 * 1000 * 1000 * 1000, // 30s
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/server -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/server
git commit -m "feat: implement HTTP handlers, CORS middleware, and concurrency limiter"
```

---

### Task 5: Main Entrypoint, CLI Runner & Integration Verification

**Files:**
- Create: `cmd/agyapi/main.go`
- Modify: `README.md` (Add full setup, flags, and verification instructions)
- Test: Build binary `go build -o bin/agyapi ./cmd/agyapi` and verify `--help` and live query against local `agy`.

**Interfaces:**
- Consumes: `config.Load()`, `server.NewServer()`.
- Produces: Executable binary `bin/agyapi`.

- [ ] **Step 1: Write main.go with graceful signal shutdown**

```go
// cmd/agyapi/main.go
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agyapi/internal/config"
	"agyapi/internal/server"
)

func main() {
	cfg := config.Load()
	flag.Parse()

	logLevel := slog.LevelInfo
	if cfg.Verbose {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))

	slog.Info("Starting agyapi service",
		"host", cfg.Host,
		"port", cfg.Port,
		"agy_path", cfg.AgyPath,
		"default_model", cfg.DefaultModel,
		"max_concurrency", cfg.MaxConcurrency,
	)

	srv := server.NewServer(cfg)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down server gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}
	slog.Info("Server stopped")
}
```

- [ ] **Step 2: Build binary and run unit tests**

Run: `go build -o bin/agyapi ./cmd/agyapi && go test ./... -v`
Expected: PASS and binary produced in `bin/agyapi`

- [ ] **Step 3: Run end-to-end verification against local agy**

Start `bin/agyapi` on port `8999` in the background, send:
1. `GET http://127.0.0.1:8999/health` -> verify status 200 OK.
2. `GET http://127.0.0.1:8999/v1/models` -> verify model list.
3. `POST http://127.0.0.1:8999/v1/chat/completions` non-streaming -> verify response content and token counts.
4. `POST http://127.0.0.1:8999/v1/chat/completions` streaming -> verify SSE chunks.

- [ ] **Step 4: Commit**

```bash
git add cmd/agyapi README.md
git commit -m "feat: complete agyapi service with main binary and usage documentation"
```
