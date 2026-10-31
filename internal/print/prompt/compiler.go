package prompt

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yaping-pro/agyapi/internal/openai"
)

// CompilePrompt compiles a slice of OpenAI chat messages and optional tool definitions
// into a structured prompt formatted for Antigravity non-interactive execution.
func CompilePrompt(messages []openai.ChatMessage, tools []openai.ToolDefinition) string {
	var sb strings.Builder

	// 1. Compile System / Developer Instructions
	var systemInstructions []string
	for _, msg := range messages {
		if msg.Role == "system" || msg.Role == "developer" {
			trimmed := strings.TrimSpace(msg.Content)
			if trimmed != "" {
				systemInstructions = append(systemInstructions, trimmed)
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
		sb.WriteString("<tool_call>\n{\"name\": \"<function_name>\", \"arguments\": <arguments_object>}\n</tool_call>\n")
		sb.WriteString("CRITICAL: Do NOT invoke any built-in/internal CLI tools (such as read_file, run_command, grep_search, list_dir, etc.). Always emit <tool_call> XML blocks in your text output and stop immediately so the caller can execute the tool.\n\n")
	}

	// 3. Compile Dialogue History
	sb.WriteString("[Conversation History]\n")
	for _, msg := range messages {
		switch msg.Role {
		case "system", "developer":
			// already placed in header
			continue
		case "user":
			sb.WriteString(fmt.Sprintf("User: %s", strings.TrimSpace(msg.Content)))
			for _, ref := range msg.ImageFiles {
				sb.WriteString(fmt.Sprintf("\n[Attached image: @%s]", ref))
			}
			sb.WriteString("\n\n")
		case "assistant":
			var text = strings.TrimSpace(msg.Content)
			if len(msg.ToolCalls) > 0 {
				var callsText []string
				for _, tc := range msg.ToolCalls {
					args := strings.TrimSpace(tc.Function.Arguments)
					if args == "" {
						args = "{}"
					} else if !json.Valid([]byte(args)) {
						if b, err := json.Marshal(args); err == nil {
							args = string(b)
						}
					}
					callsText = append(callsText, fmt.Sprintf("<tool_call>\n{\"name\": %q, \"arguments\": %s}\n</tool_call>", tc.Function.Name, args))
				}
				if text != "" {
					text = text + "\n" + strings.Join(callsText, "\n")
				} else {
					text = strings.Join(callsText, "\n")
				}
			}
			sb.WriteString(fmt.Sprintf("Assistant: %s\n\n", text))
		case "tool":
			var meta []string
			if msg.Name != "" {
				meta = append(meta, msg.Name)
			}
			if msg.ToolCallID != "" {
				meta = append(meta, fmt.Sprintf("id=%s", msg.ToolCallID))
			}
			header := "Tool"
			if len(meta) > 0 {
				header = fmt.Sprintf("Tool (%s)", strings.Join(meta, ", "))
			}
			sb.WriteString(fmt.Sprintf("%s: %s\n\n", header, strings.TrimSpace(msg.Content)))
		default:
			role := msg.Role
			if role != "" {
				r, size := utf8.DecodeRuneInString(role)
				role = string(unicode.ToUpper(r)) + role[size:]
			}
			sb.WriteString(fmt.Sprintf("%s: %s\n\n", role, strings.TrimSpace(msg.Content)))
		}
	}
	if len(tools) > 0 {
		sb.WriteString("[Reminder: Do NOT execute built-in CLI tools directly. To call any tool from [Available Tools], emit <tool_call>{\"name\": \"...\", \"arguments\": {...}}</tool_call> as text and stop.]\n\n")
	}
	sb.WriteString("Assistant: ")
	return sb.String()
}
