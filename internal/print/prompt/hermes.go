package prompt

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/yaping-pro/agyapi/internal/openai"
)

var toolCallRegex = regexp.MustCompile(`(?s)<tool_call>\s*(.*?)\s*</tool_call>`)

func generateCallID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "call_" + hex.EncodeToString(b)
}

// stripCodeFences removes markdown code block fences (e.g. ```json ... ``` or ``` ... ```) if present.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") && !strings.HasPrefix(s, "~~~") {
		return s
	}

	fenceChar := s[0]
	fenceLen := 0
	for fenceLen < len(s) && s[fenceLen] == fenceChar {
		fenceLen++
	}
	if fenceLen < 3 {
		return s
	}
	fence := s[:fenceLen]

	if newlineIdx := strings.Index(s, "\n"); newlineIdx != -1 {
		s = s[newlineIdx+1:]
	} else {
		s = s[fenceLen:]
		s = strings.TrimLeft(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-")
	}

	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, fence) {
		s = s[:len(s)-len(fence)]
	} else if strings.HasSuffix(s, string(fenceChar)+string(fenceChar)+string(fenceChar)) {
		last := strings.LastIndex(s, string(fenceChar)+string(fenceChar)+string(fenceChar))
		s = s[:last]
	}

	return strings.TrimSpace(s)
}

// ExtractHermesToolCalls parses <tool_call> XML tags into structured ToolCall slices
// and strips the tool call blocks from content, returning the cleaned content.
func ExtractHermesToolCalls(content string) (cleanedContent string, toolCalls []openai.ToolCall) {
	matches := toolCallRegex.FindAllStringSubmatch(content, -1)

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		rawJSON := stripCodeFences(match[1])
		if rawJSON == "" {
			continue
		}

		var parsed struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
			continue
		}
		if parsed.Name == "" {
			continue
		}

		argsStr := "{}"
		if len(parsed.Arguments) > 0 {
			var strVal string
			if err := json.Unmarshal(parsed.Arguments, &strVal); err == nil {
				// arguments was provided as a JSON-encoded string
				argsStr = strVal
			} else {
				trimmed := strings.TrimSpace(string(parsed.Arguments))
				if trimmed != "" && trimmed != "null" {
					argsStr = trimmed
				}
			}
		}

		argsStr = strings.TrimSpace(stripCodeFences(argsStr))
		if argsStr == "" || argsStr == "null" {
			argsStr = "{}"
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
	cleanedContent = strings.TrimSpace(cleaned)

	return cleanedContent, toolCalls
}
