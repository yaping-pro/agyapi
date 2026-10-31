package convert

import (
	"testing"

	"github.com/yaping-pro/agyapi/internal/antigravity"
	"github.com/yaping-pro/agyapi/internal/openai"
)

func testMeta() ChunkMeta {
	return ChunkMeta{ID: "chatcmpl-test", Created: 1700000000, Model: "gemini-3.8-flash-high"}
}

func TestConvertChunkTextDelta(t *testing.T) {
	resp := &antigravity.GenerateResponse{
		Candidates: []antigravity.Candidate{{
			Content: &antigravity.Content{Role: "model", Parts: []antigravity.Part{{Text: "hello"}}},
		}},
	}
	chunks, finish, usage := ConvertChunk(testMeta(), resp)
	if finish != "" {
		t.Fatalf("finishReason = %q, want empty", finish)
	}
	if usage != nil {
		t.Fatalf("usage = %+v, want nil", usage)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	if got := chunks[0].Choices[0].Delta.Content; got != "hello" {
		t.Fatalf("content = %q", got)
	}
}

func TestConvertChunkThoughtGoesToReasoning(t *testing.T) {
	resp := &antigravity.GenerateResponse{
		Candidates: []antigravity.Candidate{{
			Content: &antigravity.Content{Role: "model", Parts: []antigravity.Part{
				{Text: "pondering", Thought: true},
			}},
		}},
	}
	chunks, _, _ := ConvertChunk(testMeta(), resp)
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d", len(chunks))
	}
	d := chunks[0].Choices[0].Delta
	if d.Content != "" {
		t.Fatalf("thought leaked into content: %q", d.Content)
	}
	if d.ReasoningContent != "pondering" {
		t.Fatalf("reasoning_content = %q, want pondering", d.ReasoningContent)
	}
}

func TestConvertChunkFunctionCallBecomesToolCall(t *testing.T) {
	resp := &antigravity.GenerateResponse{
		Candidates: []antigravity.Candidate{{
			Content: &antigravity.Content{Role: "model", Parts: []antigravity.Part{{
				ThoughtSignature: "SIG-LONG",
				FunctionCall: &antigravity.FunctionCall{
					Name: "bash",
					Args: []byte(`{"command":"ls"}`),
					ID:   "call_7",
				},
			}}},
			FinishReason: "STOP",
		}},
	}
	chunks, finish, _ := ConvertChunk(testMeta(), resp)

	if finish != "tool_calls" {
		t.Fatalf("finishReason = %q, want tool_calls", finish)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	tc := chunks[0].Choices[0].Delta.ToolCalls
	if len(tc) != 1 {
		t.Fatalf("tool_calls = %d, want 1", len(tc))
	}
	if tc[0].Index == nil || *tc[0].Index != 0 {
		t.Fatalf("index = %v, want 0", tc[0].Index)
	}
	if tc[0].ID != EncodeToolID("call_7", "SIG-LONG") {
		t.Fatalf("id = %q, want signature-encoded id", tc[0].ID)
	}
	if tc[0].Function.Name != "bash" {
		t.Fatalf("name = %q", tc[0].Function.Name)
	}
	if tc[0].Function.Arguments != `{"command":"ls"}` {
		t.Fatalf("arguments = %q", tc[0].Function.Arguments)
	}
	if tc[0].Type != "function" {
		t.Fatalf("type = %q", tc[0].Type)
	}
}

func TestConvertChunkFinishReasonMapping(t *testing.T) {
	cases := map[string]string{
		"STOP":       "stop",
		"MAX_TOKENS": "length",
		"SAFETY":     "content_filter",
		"RECITATION": "content_filter",
		"OTHER":      "stop",
	}
	for upstream, want := range cases {
		resp := &antigravity.GenerateResponse{
			Candidates: []antigravity.Candidate{{FinishReason: upstream}},
		}
		_, finish, _ := ConvertChunk(testMeta(), resp)
		if finish != want {
			t.Errorf("finishReason %q -> %q, want %q", upstream, finish, want)
		}
	}
}

func TestConvertChunkUsageIsReported(t *testing.T) {
	resp := &antigravity.GenerateResponse{
		Candidates: []antigravity.Candidate{{FinishReason: "STOP"}},
		UsageMetadata: &antigravity.UsageMetadata{
			PromptTokenCount:     1200,
			CandidatesTokenCount: 34,
			TotalTokenCount:      1234,
		},
	}
	_, _, usage := ConvertChunk(testMeta(), resp)
	if usage == nil {
		t.Fatal("usage missing")
	}
	if usage.PromptTokens != 1200 || usage.CompletionTokens != 34 || usage.TotalTokens != 1234 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestConvertChunkNilResponseIsSafe(t *testing.T) {
	chunks, finish, usage := ConvertChunk(testMeta(), nil)
	if len(chunks) != 0 || finish != "" || usage != nil {
		t.Fatalf("nil response produced output: %v %q %v", chunks, finish, usage)
	}
}

func TestConvertChunkEmptyTextPartIsDropped(t *testing.T) {
	resp := &antigravity.GenerateResponse{
		Candidates: []antigravity.Candidate{{
			Content: &antigravity.Content{Role: "model", Parts: []antigravity.Part{{Text: ""}}},
		}},
	}
	chunks, _, _ := ConvertChunk(testMeta(), resp)
	if len(chunks) != 0 {
		t.Fatalf("empty text produced %d chunks", len(chunks))
	}
}

// Chunks must never carry a role after the first one, so omp treats them as
// continuations rather than fresh turns.
func TestConvertChunkNeverSetsRole(t *testing.T) {
	resp := &antigravity.GenerateResponse{
		Candidates: []antigravity.Candidate{{
			Content: &antigravity.Content{Role: "model", Parts: []antigravity.Part{{Text: "x"}}},
		}},
	}
	chunks, _, _ := ConvertChunk(testMeta(), resp)
	if got := chunks[0].Choices[0].Delta.Role; got != "" {
		t.Fatalf("role = %q, want empty", got)
	}
}

var _ = openai.ToolCall{}
