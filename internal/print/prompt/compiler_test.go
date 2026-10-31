package prompt

import (
	"strings"
	"testing"

	"github.com/yaping-pro/agyapi/internal/openai"
)

func TestCompilePrompt_BasicUserMessage(t *testing.T) {
	messages := []openai.ChatMessage{
		{Role: "user", Content: "Hello world!"},
	}

	res := CompilePrompt(messages, nil)

	if strings.Contains(res, "[System Instructions]") {
		t.Errorf("expected no system instructions header, got %s", res)
	}
	if strings.Contains(res, "[Available Tools]") {
		t.Errorf("expected no available tools header, got %s", res)
	}
	if !strings.Contains(res, "[Conversation History]\n") {
		t.Errorf("expected conversation history header, got %s", res)
	}
	if !strings.Contains(res, "User: Hello world!\n\n") {
		t.Errorf("expected 'User: Hello world!\\n\\n', got %s", res)
	}
	if !strings.HasSuffix(res, "Assistant: ") {
		t.Errorf("expected trailing 'Assistant: ', got %s", res)
	}
}

func TestCompilePrompt_SystemAndDeveloperInstructions(t *testing.T) {
	messages := []openai.ChatMessage{
		{Role: "system", Content: "You are a helpful coding assistant."},
		{Role: "developer", Content: "Strictly adhere to TDD."},
		{Role: "user", Content: "Write a function."},
	}

	res := CompilePrompt(messages, nil)

	if !strings.Contains(res, "[System Instructions]\nYou are a helpful coding assistant.\n\nStrictly adhere to TDD.\n\n") {
		t.Errorf("unexpected system instructions in: %s", res)
	}

	// Verify system and developer messages are NOT in Conversation History
	convHistory := res[strings.Index(res, "[Conversation History]"):]
	if strings.Contains(convHistory, "System:") || strings.Contains(convHistory, "Developer:") {
		t.Errorf("system or developer role leaked into conversation history: %s", convHistory)
	}
	if !strings.Contains(convHistory, "User: Write a function.\n\nAssistant: ") {
		t.Errorf("unexpected conversation history: %s", convHistory)
	}
}

func TestCompilePrompt_AvailableTools(t *testing.T) {
	messages := []openai.ChatMessage{
		{Role: "user", Content: "What is the weather in Paris?"},
	}
	tools := []openai.ToolDefinition{
		{
			Type: "function",
			Function: openai.ToolFunctionDef{
				Name:        "get_weather",
				Description: "Get the current weather for a city",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
					"required": []string{"city"},
				},
			},
		},
	}

	res := CompilePrompt(messages, tools)

	if !strings.Contains(res, "[Available Tools]\n") {
		t.Fatalf("expected '[Available Tools]', got %s", res)
	}
	if !strings.Contains(res, `"name": "get_weather"`) {
		t.Errorf("expected tool definition to contain get_weather, got %s", res)
	}
	if !strings.Contains(res, "To call one or more tools, format your response strictly as:\n<tool_call>\n{\"name\": \"<function_name>\", \"arguments\": <arguments_object>}\n</tool_call>") {
		t.Errorf("expected tool call instructions, got %s", res)
	}
}

func TestCompilePrompt_MultiTurnWithToolCallsAndResponse(t *testing.T) {
	messages := []openai.ChatMessage{
		{Role: "user", Content: "What's the weather in Tokyo?"},
		{
			Role:    "assistant",
			Content: "Let me check Tokyo's weather.",
			ToolCalls: []openai.ToolCall{
				{
					ID:   "call_abc123",
					Type: "function",
					Function: openai.FunctionCall{
						Name:      "get_weather",
						Arguments: `{"city": "Tokyo"}`,
					},
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call_abc123",
			Content:    `{"temperature": 22, "condition": "Sunny"}`,
		},
		{
			Role:    "assistant",
			Content: "The weather in Tokyo is sunny and 22°C.",
		},
		{
			Role:    "user",
			Content: "Thank you!",
		},
	}

	res := CompilePrompt(messages, nil)

	expectedAssistantCall := "Assistant: Let me check Tokyo's weather.\n<tool_call>\n{\"name\": \"get_weather\", \"arguments\": {\"city\": \"Tokyo\"}}\n</tool_call>\n\n"
	if !strings.Contains(res, expectedAssistantCall) {
		t.Errorf("expected assistant tool call block:\n%s\n\nGot:\n%s", expectedAssistantCall, res)
	}

	expectedToolResponse := "Tool (id=call_abc123): {\"temperature\": 22, \"condition\": \"Sunny\"}\n\n"
	if !strings.Contains(res, expectedToolResponse) {
		t.Errorf("expected tool response block:\n%s\n\nGot:\n%s", expectedToolResponse, res)
	}

	if !strings.Contains(res, "Assistant: The weather in Tokyo is sunny and 22°C.\n\n") {
		t.Errorf("expected second assistant message, got %s", res)
	}
	if !strings.Contains(res, "User: Thank you!\n\n") {
		t.Errorf("expected final user message, got %s", res)
	}
	if !strings.HasSuffix(res, "Assistant: ") {
		t.Errorf("expected trailing 'Assistant: ', got %s", res)
	}
}

func TestCompilePrompt_AssistantToolCallsWithoutContent(t *testing.T) {
	messages := []openai.ChatMessage{
		{
			Role: "assistant",
			ToolCalls: []openai.ToolCall{
				{
					ID:   "call_xyz",
					Type: "function",
					Function: openai.FunctionCall{
						Name:      "ping",
						Arguments: "{}",
					},
				},
			},
		},
	}

	res := CompilePrompt(messages, nil)
	expected := "Assistant: <tool_call>\n{\"name\": \"ping\", \"arguments\": {}}\n</tool_call>\n\nAssistant: "
	if !strings.Contains(res, expected) {
		t.Errorf("expected:\n%s\n\nGot:\n%s", expected, res)
	}
}

func TestCompilePrompt_ToolResponseWithoutID(t *testing.T) {
	messages := []openai.ChatMessage{
		{
			Role:    "tool",
			Content: "Success",
		},
	}

	res := CompilePrompt(messages, nil)
	expected := "Tool: Success\n\n"
	if !strings.Contains(res, expected) {
		t.Errorf("expected '%s', got '%s'", expected, res)
	}
}

func TestCompilePrompt_ToolResponseWithNameAndID(t *testing.T) {
	messages := []openai.ChatMessage{
		{
			Role:       "tool",
			Name:       "get_weather",
			ToolCallID: "call_abc123",
			Content:    "Sunny",
		},
	}

	res := CompilePrompt(messages, nil)
	expected := "Tool (get_weather, id=call_abc123): Sunny\n\n"
	if !strings.Contains(res, expected) {
		t.Errorf("expected '%s', got '%s'", expected, res)
	}
}

func TestCompilePrompt_CustomRole(t *testing.T) {
	messages := []openai.ChatMessage{
		{
			Role:    "custom",
			Content: "Custom instruction",
		},
		{
			Role:    "über",
			Content: "Unicode role instruction",
		},
	}

	res := CompilePrompt(messages, nil)
	expected1 := "Custom: Custom instruction\n\n"
	expected2 := "Über: Unicode role instruction\n\n"
	if !strings.Contains(res, expected1) {
		t.Errorf("expected '%s', got '%s'", expected1, res)
	}
	if !strings.Contains(res, expected2) {
		t.Errorf("expected '%s', got '%s'", expected2, res)
	}
}
