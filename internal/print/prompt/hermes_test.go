package prompt

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractHermesToolCalls_SingleTool(t *testing.T) {
	raw := `I will check the weather for you.
<tool_call>
{"name": "get_weather", "arguments": {"location": "Paris, France"}}
</tool_call>`

	cleaned, toolCalls := ExtractHermesToolCalls(raw)
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if !strings.HasPrefix(toolCalls[0].ID, "call_") {
		t.Errorf("expected tool call ID to start with 'call_', got '%s'", toolCalls[0].ID)
	}
	if toolCalls[0].Type != "function" {
		t.Errorf("expected type 'function', got '%s'", toolCalls[0].Type)
	}
	if toolCalls[0].Function.Name != "get_weather" {
		t.Errorf("expected 'get_weather', got '%s'", toolCalls[0].Function.Name)
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(toolCalls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("failed to unmarshal function arguments: %v", err)
	}
	if args["location"] != "Paris, France" {
		t.Errorf("expected location 'Paris, France', got '%v'", args["location"])
	}

	if cleaned != "I will check the weather for you." {
		t.Errorf("unexpected cleaned content: '%s'", cleaned)
	}
}

func TestExtractHermesToolCalls_NoTools(t *testing.T) {
	raw := "Just a normal answer with no tool calls."
	cleaned, toolCalls := ExtractHermesToolCalls(raw)
	if len(toolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(toolCalls))
	}
	if cleaned != raw {
		t.Errorf("expected '%s', got '%s'", raw, cleaned)
	}
}

func TestExtractHermesToolCalls_MultipleTools(t *testing.T) {
	raw := `Thinking: let's query both systems.
<tool_call>
{"name": "get_weather", "arguments": {"location": "Tokyo"}}
</tool_call>
Intermediate note.
<tool_call>
{"name": "get_time", "arguments": {"timezone": "Asia/Tokyo"}}
</tool_call>
Final commentary.`

	cleaned, toolCalls := ExtractHermesToolCalls(raw)
	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(toolCalls))
	}
	if toolCalls[0].ID == toolCalls[1].ID {
		t.Errorf("tool call IDs should be unique, got duplicate '%s'", toolCalls[0].ID)
	}
	if toolCalls[0].Function.Name != "get_weather" {
		t.Errorf("expected 'get_weather', got '%s'", toolCalls[0].Function.Name)
	}
	if toolCalls[1].Function.Name != "get_time" {
		t.Errorf("expected 'get_time', got '%s'", toolCalls[1].Function.Name)
	}

	if strings.Contains(cleaned, "<tool_call>") || strings.Contains(cleaned, "</tool_call>") {
		t.Errorf("cleaned text should not contain tool_call tags, got '%s'", cleaned)
	}
	if !strings.Contains(cleaned, "Thinking: let's query both systems.") {
		t.Errorf("cleaned text missing preamble: '%s'", cleaned)
	}
	if !strings.Contains(cleaned, "Final commentary.") {
		t.Errorf("cleaned text missing postamble: '%s'", cleaned)
	}
}

func TestExtractHermesToolCalls_WhitespaceAndFormatting(t *testing.T) {
	raw := `Result follows:
<tool_call>
  {
    "name": "calculate",
    "arguments": {
      "expression": "42 * 2"
    }
  }
</tool_call>`

	cleaned, toolCalls := ExtractHermesToolCalls(raw)
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Function.Name != "calculate" {
		t.Errorf("expected 'calculate', got '%s'", toolCalls[0].Function.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(toolCalls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("failed to unmarshal arguments: %v", err)
	}
	if args["expression"] != "42 * 2" {
		t.Errorf("expected '42 * 2', got '%v'", args["expression"])
	}
	if cleaned != "Result follows:" {
		t.Errorf("unexpected cleaned content: '%s'", cleaned)
	}
}

func TestExtractHermesToolCalls_StringArguments(t *testing.T) {
	raw := `<tool_call>
{"name": "fetch_url", "arguments": "{\"url\": \"https://example.com\"}"}
</tool_call>`

	_, toolCalls := ExtractHermesToolCalls(raw)
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Function.Name != "fetch_url" {
		t.Errorf("expected 'fetch_url', got '%s'", toolCalls[0].Function.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(toolCalls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("arguments should be valid JSON: %v, got %s", err, toolCalls[0].Function.Arguments)
	}
	if args["url"] != "https://example.com" {
		t.Errorf("expected url 'https://example.com', got '%v'", args["url"])
	}
}

func TestExtractHermesToolCalls_NoArguments(t *testing.T) {
	raw := `<tool_call>
{"name": "get_random_uuid"}
</tool_call>`

	_, toolCalls := ExtractHermesToolCalls(raw)
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Function.Name != "get_random_uuid" {
		t.Errorf("expected 'get_random_uuid', got '%s'", toolCalls[0].Function.Name)
	}
	if toolCalls[0].Function.Arguments != "{}" {
		t.Errorf("expected '{}', got '%s'", toolCalls[0].Function.Arguments)
	}
}

func TestExtractHermesToolCalls_MalformedJSON(t *testing.T) {
	raw := `Here is something invalid:
<tool_call>
not valid json at all
</tool_call>
Continuing response.`

	cleaned, toolCalls := ExtractHermesToolCalls(raw)
	if len(toolCalls) != 0 {
		t.Errorf("expected 0 tool calls for malformed JSON, got %d", len(toolCalls))
	}
	if strings.Contains(cleaned, "<tool_call>") || strings.Contains(cleaned, "</tool_call>") {
		t.Errorf("cleaned content should strip tool_call tags, got '%s'", cleaned)
	}
	if !strings.Contains(cleaned, "Continuing response.") {
		t.Errorf("cleaned content should retain commentary, got '%s'", cleaned)
	}
}

func TestExtractHermesToolCalls_EmptyAndWhitespaceArguments(t *testing.T) {
	testCases := []struct {
		name string
		raw  string
	}{
		{
			name: "empty string argument",
			raw: `<tool_call>
{"name": "do_work", "arguments": ""}
</tool_call>`,
		},
		{
			name: "whitespace string argument",
			raw: `<tool_call>
{"name": "do_work", "arguments": "   \n\t  "}
</tool_call>`,
		},
		{
			name: "null argument",
			raw: `<tool_call>
{"name": "do_work", "arguments": null}
</tool_call>`,
		},
		{
			name: "empty object argument",
			raw: `<tool_call>
{"name": "do_work", "arguments": {}}
</tool_call>`,
		},
		{
			name: "omitted arguments",
			raw: `<tool_call>
{"name": "do_work"}
</tool_call>`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cleaned, toolCalls := ExtractHermesToolCalls(tc.raw)
			if len(toolCalls) != 1 {
				t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
			}
			if toolCalls[0].Function.Name != "do_work" {
				t.Errorf("expected 'do_work', got '%s'", toolCalls[0].Function.Name)
			}
			if toolCalls[0].Function.Arguments != "{}" {
				t.Errorf("expected arguments '{}', got '%s'", toolCalls[0].Function.Arguments)
			}
			var parsed map[string]any
			if err := json.Unmarshal([]byte(toolCalls[0].Function.Arguments), &parsed); err != nil {
				t.Errorf("arguments must be valid JSON: %v", err)
			}
			if cleaned != "" {
				t.Errorf("expected empty cleaned content, got '%s'", cleaned)
			}
		})
	}
}

func TestExtractHermesToolCalls_MarkdownCodeFences(t *testing.T) {
	testCases := []struct {
		name         string
		raw          string
		expectedName string
		expectedArg  string
	}{
		{
			name:         "triple backticks with json tag",
			raw:          "Preamble\n<tool_call>\n```json\n{\"name\": \"get_weather\", \"arguments\": {\"location\": \"Paris\"}}\n```\n</tool_call>\nPostamble",
			expectedName: "get_weather",
			expectedArg:  "Paris",
		},
		{
			name:         "triple backticks without language tag",
			raw:          "<tool_call>\n```\n{\"name\": \"get_weather\", \"arguments\": {\"location\": \"London\"}}\n```\n</tool_call>",
			expectedName: "get_weather",
			expectedArg:  "London",
		},
		{
			name:         "quadruple backticks with json tag",
			raw:          "<tool_call>\n````json\n{\"name\": \"get_weather\", \"arguments\": {\"location\": \"Tokyo\"}}\n````\n</tool_call>",
			expectedName: "get_weather",
			expectedArg:  "Tokyo",
		},
		{
			name:         "single-line code fence",
			raw:          "<tool_call>```json {\"name\": \"get_weather\", \"arguments\": {\"location\": \"Berlin\"}} ```</tool_call>",
			expectedName: "get_weather",
			expectedArg:  "Berlin",
		},
		{
			name:         "code fence with empty arguments defaulting to {}",
			raw:          "<tool_call>\n```json\n{\"name\": \"ping\", \"arguments\": \"  \"}\n```\n</tool_call>",
			expectedName: "ping",
			expectedArg:  "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cleaned, toolCalls := ExtractHermesToolCalls(tc.raw)
			if len(toolCalls) != 1 {
				t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
			}
			if toolCalls[0].Function.Name != tc.expectedName {
				t.Errorf("expected function name '%s', got '%s'", tc.expectedName, toolCalls[0].Function.Name)
			}

			var parsed map[string]any
			if err := json.Unmarshal([]byte(toolCalls[0].Function.Arguments), &parsed); err != nil {
				t.Fatalf("arguments must be valid JSON: %v, got %s", err, toolCalls[0].Function.Arguments)
			}

			if tc.expectedArg != "" {
				if parsed["location"] != tc.expectedArg {
					t.Errorf("expected location '%s', got '%v'", tc.expectedArg, parsed["location"])
				}
			} else {
				if toolCalls[0].Function.Arguments != "{}" {
					t.Errorf("expected '{}', got '%s'", toolCalls[0].Function.Arguments)
				}
			}

			if strings.Contains(cleaned, "<tool_call>") || strings.Contains(cleaned, "</tool_call>") {
				t.Errorf("cleaned content should not have tool call tags: '%s'", cleaned)
			}
			if strings.Contains(cleaned, "```") {
				t.Errorf("cleaned content should not contain stripped code fences: '%s'", cleaned)
			}
		})
	}
}
