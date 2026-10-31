package convert

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yaping-pro/agyapi/internal/antigravity"
	"github.com/yaping-pro/agyapi/internal/openai"
)

func testOptions() BuildOptions {
	return BuildOptions{
		Project:   "aicode-consumers",
		RequestID: "req-test",
		SessionID: "-12345",
	}
}

// Root cause #3: role=developer messages must stay in the conversation history
// instead of being hoisted into the top-level system instruction.
func TestBuildRequestKeepsDeveloperInHistory(t *testing.T) {
	req := openai.ChatCompletionRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{
			{Role: "system", Content: "SYS-A"},
			{Role: "user", Content: "hello"},
			{Role: "developer", Content: "<system-reminder>todos</system-reminder>"},
			{Role: "user", Content: "go on"},
		},
	}

	got, err := BuildRequest(req, testOptions())
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}

	if got.Request.SystemInstruction == nil {
		t.Fatal("systemInstruction missing")
	}
	sys := partsText(got.Request.SystemInstruction.Parts)
	if strings.Contains(sys, "system-reminder") {
		t.Fatalf("developer leaked into systemInstruction: %q", sys)
	}
	if !strings.Contains(sys, "SYS-A") {
		t.Fatalf("system prompt lost: %q", sys)
	}

	history := flattenContents(got.Request.Contents)
	if !strings.Contains(history, "system-reminder") {
		t.Fatalf("developer message dropped from history: %q", history)
	}
	if strings.Index(history, "hello") > strings.Index(history, "system-reminder") {
		t.Fatalf("history order broken: %q", history)
	}
}

// Root cause #2/#4: tools are first-class function declarations.
func TestBuildRequestEmitsFunctionDeclarationsWithCleanedSchema(t *testing.T) {
	req := openai.ChatCompletionRequest{
		Model:    "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{{Role: "user", Content: "read a file"}},
		Tools: []openai.ToolDefinition{{
			Type: "function",
			Function: openai.ToolFunctionDef{
				Name:        "read",
				Description: "Read a file",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string"},
					},
					"required": []any{"path"},
					"$schema":  "https://json-schema.org/draft/2020-12/schema",
				},
			},
		}},
	}

	got, err := BuildRequest(req, testOptions())
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}

	if len(got.Request.Tools) != 1 || len(got.Request.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools = %+v, want one declaration", got.Request.Tools)
	}
	decl := got.Request.Tools[0].FunctionDeclarations[0]
	if decl.Name != "read" {
		t.Fatalf("name = %q", decl.Name)
	}
	if decl.ParametersJSONSchema == nil {
		t.Fatal("parametersJsonSchema missing")
	}
	if _, ok := decl.ParametersJSONSchema["$schema"]; ok {
		t.Fatalf("$schema not cleaned: %v", decl.ParametersJSONSchema)
	}
	if got.Request.ToolConfig == nil || got.Request.ToolConfig.FunctionCallingConfig.Mode != "VALIDATED" {
		t.Fatalf("toolConfig = %+v, want mode VALIDATED", got.Request.ToolConfig)
	}
}

// The thoughtSignature must survive the client round-trip through the tool id.
func TestBuildRequestDecodesSignatureFromToolID(t *testing.T) {
	const (
		rawID = "call_1"
		sig   = "SIG-ABC"
	)
	encoded := EncodeToolID(rawID, sig)

	req := openai.ChatCompletionRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "list files"},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []openai.ToolCall{{
					ID:   encoded,
					Type: "function",
					Function: openai.FunctionCall{
						Name:      "bash",
						Arguments: `{"command":"ls"}`,
					},
				}},
			},
			{Role: "tool", ToolCallID: encoded, Name: "bash", Content: "file.txt"},
		},
	}

	got, err := BuildRequest(req, testOptions())
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if len(got.Request.Contents) != 3 {
		t.Fatalf("contents = %d, want 3", len(got.Request.Contents))
	}

	callPart := got.Request.Contents[1].Parts[0]
	if callPart.FunctionCall == nil {
		t.Fatalf("expected functionCall part, got %+v", callPart)
	}
	if callPart.FunctionCall.ID != rawID {
		t.Fatalf("functionCall.id = %q, want %q", callPart.FunctionCall.ID, rawID)
	}
	if callPart.ThoughtSignature != sig {
		t.Fatalf("thoughtSignature = %q, want %q", callPart.ThoughtSignature, sig)
	}

	respPart := got.Request.Contents[2].Parts[0]
	if respPart.FunctionResponse == nil {
		t.Fatalf("expected functionResponse part, got %+v", respPart)
	}
	if respPart.FunctionResponse.ID != rawID {
		t.Fatalf("functionResponse.id = %q, want %q", respPart.FunctionResponse.ID, rawID)
	}
	if respPart.FunctionResponse.Name != "bash" {
		t.Fatalf("functionResponse.name = %q", respPart.FunctionResponse.Name)
	}
	var payload map[string]any
	if err := json.Unmarshal(respPart.FunctionResponse.Response, &payload); err != nil {
		t.Fatalf("functionResponse.response is not JSON: %v", err)
	}
	if payload["output"] != "file.txt" {
		t.Fatalf("functionResponse.response = %v", payload)
	}
}

func TestBuildRequestEnvelope(t *testing.T) {
	req := openai.ChatCompletionRequest{
		Model:    "gemini-3.8-flash-medium",
		Messages: []openai.ChatMessage{{Role: "user", Content: "hi"}},
	}
	got, err := BuildRequest(req, testOptions())
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got.Project != "aicode-consumers" {
		t.Fatalf("project = %q", got.Project)
	}
	if got.RequestType != "agent" {
		t.Fatalf("requestType = %q, want agent", got.RequestType)
	}
	if got.Model != "gemini-3.8-flash-tiered" {
		t.Fatalf("model = %q, want gemini-3.8-flash-tiered", got.Model)
	}
	if got.Request.SessionID != "-12345" {
		t.Fatalf("sessionId = %q", got.Request.SessionID)
	}
}

func TestResolveModel(t *testing.T) {
	cases := []struct {
		in       string
		upstream string
		level    string
	}{
		{"gemini-3.8-flash-high", "gemini-3.8-flash-tiered", "HIGH"},
		{"gemini-3.8-flash-medium", "gemini-3.8-flash-tiered", "MEDIUM"},
		{"gemini-3.8-flash-low", "gemini-3.8-flash-tiered", "LOW"},
		{"gemini-3.8-flash", "gemini-3.8-flash-tiered", "MEDIUM"},
		{"gemini-4-argon-high", "gemini-3.8-flash-tiered", "HIGH"},
		{"claude-sonnet-4-6", "claude-sonnet-4-6", ""},
		{"claude-opus-4-6", "claude-opus-4-6-thinking", ""},
		{"claude-sonnet-5-5-medium", "claude-sonnet-4-6", ""},
		{"gpt-oss-120b", "gpt-oss-120b-medium", ""},
		{"gemini-3-flash", "gemini-3-flash", ""},
	}
	for _, c := range cases {
		p := ResolveModel(c.in)
		if p.Upstream != c.upstream {
			t.Errorf("ResolveModel(%q).Upstream = %q, want %q", c.in, p.Upstream, c.upstream)
		}
		if p.ThinkingLevel != c.level {
			t.Errorf("ResolveModel(%q).ThinkingLevel = %q, want %q", c.in, p.ThinkingLevel, c.level)
		}
	}
}

func TestBuildRequestAppliesThinkingLevel(t *testing.T) {
	req := openai.ChatCompletionRequest{
		Model:    "gemini-3.8-flash-high",
		Messages: []openai.ChatMessage{{Role: "user", Content: "hi"}},
	}
	got, err := BuildRequest(req, testOptions())
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got.Request.GenerationConfig == nil || got.Request.GenerationConfig.ThinkingConfig == nil {
		t.Fatalf("thinkingConfig missing: %+v", got.Request.GenerationConfig)
	}
	tc := got.Request.GenerationConfig.ThinkingConfig
	if tc.ThinkingLevel != "HIGH" {
		t.Fatalf("thinkingLevel = %q, want HIGH", tc.ThinkingLevel)
	}
	if !tc.IncludeThoughts {
		t.Fatal("includeThoughts should be true so reasoning is streamed back")
	}
}

func partsText(parts []antigravity.Part) string {
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(p.Text)
	}
	return sb.String()
}

func flattenContents(contents []antigravity.Content) string {
	var sb strings.Builder
	for _, c := range contents {
		for _, p := range c.Parts {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// OpenAI clients send only tool_call_id on tool messages; the response name
// must be recovered from the issuing assistant turn or upstream rejects it.
func TestBuildRequestResolvesToolNameFromHistory(t *testing.T) {
	encoded := EncodeToolID("call_9", "SIG")
	req := openai.ChatCompletionRequest{
		Model: "gemini-3.8-flash-low",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "read it"},
			{Role: "assistant", ToolCalls: []openai.ToolCall{{
				ID: encoded, Type: "function",
				Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"/tmp/x"}`},
			}}},
			{Role: "tool", ToolCallID: encoded, Content: "contents"}, // no Name
		},
	}
	got, err := BuildRequest(req, testOptions())
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	fr := got.Request.Contents[2].Parts[0].FunctionResponse
	if fr == nil {
		t.Fatalf("missing functionResponse: %+v", got.Request.Contents[2])
	}
	if fr.Name != "read" {
		t.Fatalf("functionResponse.name = %q, want read", fr.Name)
	}
	if fr.ID != "call_9" {
		t.Fatalf("functionResponse.id = %q, want call_9", fr.ID)
	}
}
