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
	if msg.Role != "user" {
		t.Errorf("expected role 'user', got '%s'", msg.Role)
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
	if msg.Role != "user" {
		t.Errorf("expected role 'user', got '%s'", msg.Role)
	}
	if msg.Content != "part1 part2" {
		t.Errorf("expected 'part1 part2', got '%s'", msg.Content)
	}
}

func TestParseMessageContent_NilContent(t *testing.T) {
	raw := `{"role": "assistant", "content": null}`
	var msg ChatMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("failed to unmarshal nil content: %v", err)
	}
	if msg.Role != "assistant" {
		t.Errorf("expected role 'assistant', got '%s'", msg.Role)
	}
	if msg.Content != "" {
		t.Errorf("expected empty string content, got '%s'", msg.Content)
	}
}

func TestParseMessageContent_ToolCalls(t *testing.T) {
	raw := `{
		"role": "assistant",
		"content": null,
		"tool_calls": [
			{
				"id": "call_123",
				"type": "function",
				"function": {
					"name": "get_weather",
					"arguments": "{\"location\": \"Paris\"}"
				}
			}
		]
	}`
	var msg ChatMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("failed to unmarshal tool call message: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	call := msg.ToolCalls[0]
	if call.ID != "call_123" {
		t.Errorf("expected id 'call_123', got '%s'", call.ID)
	}
	if call.Type != "function" {
		t.Errorf("expected type 'function', got '%s'", call.Type)
	}
	if call.Function.Name != "get_weather" {
		t.Errorf("expected func name 'get_weather', got '%s'", call.Function.Name)
	}
	if call.Function.Arguments != `{"location": "Paris"}` {
		t.Errorf("expected arguments '{\"location\": \"Paris\"}', got '%s'", call.Function.Arguments)
	}
}

func TestChatCompletionRequest_Unmarshal(t *testing.T) {
	raw := `{
		"model": "gemini-3.8-flash-high",
		"messages": [{"role": "user", "content": "hi"}],
		"stream": true,
		"temperature": 0.7,
		"max_tokens": 100,
		"tools": [
			{
				"type": "function",
				"function": {
					"name": "search",
					"description": "web search"
				}
			}
		]
	}`
	var req ChatCompletionRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}
	if req.Model != "gemini-3.8-flash-high" {
		t.Errorf("expected model 'gemini-3.8-flash-high', got '%s'", req.Model)
	}
	if !req.Stream {
		t.Errorf("expected stream=true")
	}
	if req.Temperature == nil || *req.Temperature != 0.7 {
		t.Errorf("expected temperature 0.7")
	}
	if req.MaxTokens == nil || *req.MaxTokens != 100 {
		t.Errorf("expected max_tokens 100")
	}
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "search" {
		t.Errorf("expected tools with function 'search'")
	}
}

func TestChatCompletionResponse_Marshal(t *testing.T) {
	resp := ChatCompletionResponse{
		ID:      "chatcmpl-test",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "gemini-3.8-flash-high",
		Choices: []ChatChoice{
			{
				Index: 0,
				Message: ChatMessage{
					Role:    "assistant",
					Content: "Hello there!",
				},
				FinishReason: "stop",
			},
		},
		Usage: UsageInfo{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}
	bytes, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var parsed ChatCompletionResponse
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("failed to unmarshal marshaled response: %v", err)
	}
	if parsed.ID != resp.ID {
		t.Errorf("expected ID '%s', got '%s'", resp.ID, parsed.ID)
	}
	if len(parsed.Choices) != 1 || parsed.Choices[0].Message.Content != "Hello there!" {
		t.Errorf("unexpected choice content")
	}
	if parsed.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop', got '%s'", parsed.Choices[0].FinishReason)
	}
}

func TestChatCompletionChunk_Marshal(t *testing.T) {
	stop := "stop"
	chunk := ChatCompletionChunk{
		ID:      "chatcmpl-chunk-test",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gemini-3.8-flash-high",
		Choices: []ChunkChoice{
			{
				Index: 0,
				Delta: ChunkDelta{
					Content: "partial output",
				},
				FinishReason: &stop,
			},
		},
	}
	bytes, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("failed to marshal chunk: %v", err)
	}

	var parsed ChatCompletionChunk
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("failed to unmarshal chunk: %v", err)
	}
	if parsed.Choices[0].Delta.Content != "partial output" {
		t.Errorf("expected delta content 'partial output', got '%s'", parsed.Choices[0].Delta.Content)
	}
	if parsed.Choices[0].FinishReason == nil || *parsed.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop'")
	}
}

func TestModelListResponse_Marshal(t *testing.T) {
	list := ModelListResponse{
		Object: "list",
		Data: []ModelObject{
			{
				ID:      "gemini-3.8-flash-high",
				Object:  "model",
				Created: 1725980000,
				OwnedBy: "antigravity",
			},
		},
	}
	bytes, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("failed to marshal model list: %v", err)
	}

	var parsed ModelListResponse
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("failed to unmarshal model list: %v", err)
	}
	if len(parsed.Data) != 1 || parsed.Data[0].ID != "gemini-3.8-flash-high" {
		t.Errorf("unexpected model data in unmarshaled response")
	}
}

func TestErrorResponse_Marshal(t *testing.T) {
	param := "model"
	errResp := ErrorResponse{
		Error: APIError{
			Message: "The model 'unknown' does not exist",
			Type:    "invalid_request_error",
			Param:   &param,
			Code:    "model_not_found",
		},
	}
	bytes, err := json.Marshal(errResp)
	if err != nil {
		t.Fatalf("failed to marshal ErrorResponse: %v", err)
	}

	var parsed ErrorResponse
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("failed to unmarshal ErrorResponse: %v", err)
	}
	if parsed.Error.Message != errResp.Error.Message {
		t.Errorf("expected message '%s', got '%s'", errResp.Error.Message, parsed.Error.Message)
	}
	if parsed.Error.Type != "invalid_request_error" {
		t.Errorf("expected type 'invalid_request_error', got '%s'", parsed.Error.Type)
	}
	if parsed.Error.Param == nil || *parsed.Error.Param != "model" {
		t.Errorf("expected param 'model', got '%v'", parsed.Error.Param)
	}
	if parsed.Error.Code != "model_not_found" {
		t.Errorf("expected code 'model_not_found', got '%v'", parsed.Error.Code)
	}
}
