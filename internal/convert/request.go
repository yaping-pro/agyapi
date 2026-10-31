package convert

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yaping-pro/agyapi/internal/antigravity"
	"github.com/yaping-pro/agyapi/internal/openai"
)

// BuildOptions carries per-request envelope values.
type BuildOptions struct {
	Project   string
	RequestID string
	SessionID string
}

// BuildRequest converts an OpenAI chat request into the v1internal envelope.
//
// The conversion is structural, not textual: tools stay function declarations
// and history stays a contents[] array, so nothing is truncated and no
// mid-conversation system reminder gets promoted to the top of the prompt.
func BuildRequest(req openai.ChatCompletionRequest, opt BuildOptions) (*antigravity.GenerateRequest, error) {
	profile := ResolveModel(req.Model)

	systemText, history := splitMessages(req.Messages)

	contents, err := buildContents(history)
	if err != nil {
		return nil, err
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("no user-visible messages after conversion")
	}

	genReq := antigravity.GeminiRequest{
		Contents:  contents,
		SessionID: opt.SessionID,
	}

	if systemText != "" {
		genReq.SystemInstruction = &antigravity.Content{
			Parts: []antigravity.Part{{Text: systemText}},
		}
	}

	if tools, calls := buildTools(req.Tools); len(tools) > 0 {
		genReq.Tools = tools
		genReq.ToolConfig = &antigravity.ToolConfig{
			FunctionCallingConfig: antigravity.FunctionCallingConfig{Mode: "VALIDATED"},
		}
		_ = calls
	}

	genReq.GenerationConfig = buildGenerationConfig(req, profile)

	return &antigravity.GenerateRequest{
		Project:     opt.Project,
		RequestID:   opt.RequestID,
		RequestType: "agent",
		Model:       profile.Upstream,
		Request:     genReq,
	}, nil
}

// splitMessages separates true system prompts from everything that must stay
// in conversational order. role=developer deliberately stays in history: it is
// a point-in-time harness reminder, not a global instruction.
func splitMessages(msgs []openai.ChatMessage) (systemText string, history []openai.ChatMessage) {
	var sysLines []string
	for _, m := range msgs {
		switch m.Role {
		case "system":
			if t := strings.TrimSpace(m.Content); t != "" {
				sysLines = append(sysLines, t)
			}
		default:
			history = append(history, m)
		}
	}
	return strings.Join(sysLines, "\n\n"), history
}

func buildContents(msgs []openai.ChatMessage) ([]antigravity.Content, error) {
	out := make([]antigravity.Content, 0, len(msgs))
	// Gemini requires functionResponse.name to match the invoked function, but
	// OpenAI clients only send tool_call_id. Recover the name from the issuing
	// assistant turn.
	toolNames := make(map[string]string)

	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			parts := assistantParts(m)
			if len(parts) == 0 {
				continue
			}
			for _, tc := range m.ToolCalls {
				rawID, _ := DecodeToolID(tc.ID)
				if rawID != "" && tc.Function.Name != "" {
					toolNames[rawID] = tc.Function.Name
				}
			}
			out = append(out, antigravity.Content{Role: "model", Parts: parts})

		case "tool":
			parts, err := toolResultParts(m, toolNames)
			if err != nil {
				return nil, err
			}
			out = append(out, antigravity.Content{Role: "user", Parts: parts})

		default: // user, developer, and any unknown role
			parts, err := userParts(m)
			if err != nil {
				return nil, err
			}
			if len(parts) == 0 {
				continue
			}
			out = append(out, antigravity.Content{Role: "user", Parts: parts})
		}
	}
	return out, nil
}

func assistantParts(m openai.ChatMessage) []antigravity.Part {
	var parts []antigravity.Part
	if text := strings.TrimSpace(m.Content); text != "" {
		parts = append(parts, antigravity.Part{Text: text})
	}
	for _, tc := range m.ToolCalls {
		rawID, sig := DecodeToolID(tc.ID)
		args := strings.TrimSpace(tc.Function.Arguments)
		if args == "" || !json.Valid([]byte(args)) {
			args = "{}"
		}
		parts = append(parts, antigravity.Part{
			ThoughtSignature: sig,
			FunctionCall: &antigravity.FunctionCall{
				Name: tc.Function.Name,
				Args: json.RawMessage(args),
				ID:   rawID,
			},
		})
	}
	return parts
}

func toolResultParts(m openai.ChatMessage, toolNames map[string]string) ([]antigravity.Part, error) {
	rawID, _ := DecodeToolID(m.ToolCallID)
	name := m.Name
	if name == "" {
		name = toolNames[rawID]
	}
	if name == "" {
		name = toolNames[m.ToolCallID]
	}
	if name == "" {
		name = "tool"
	}
	payload, err := json.Marshal(map[string]any{"output": m.Content})
	if err != nil {
		return nil, fmt.Errorf("marshal tool result: %w", err)
	}
	return []antigravity.Part{{
		FunctionResponse: &antigravity.FunctionResponse{
			Name:     name,
			Response: payload,
			ID:       rawID,
		},
	}}, nil
}

func userParts(m openai.ChatMessage) ([]antigravity.Part, error) {
	var parts []antigravity.Part
	if text := strings.TrimSpace(m.Content); text != "" {
		parts = append(parts, antigravity.Part{Text: text})
	}
	for _, path := range m.ImageFiles {
		inline, err := inlineFromFile(path)
		if err != nil {
			return nil, err
		}
		parts = append(parts, antigravity.Part{InlineData: inline})
	}
	return parts, nil
}

func inlineFromFile(path string) (*antigravity.InlineData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read image %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("image %s is empty", path)
	}
	return &antigravity.InlineData{
		MimeType: mimeForExt(filepath.Ext(path)),
		Data:     base64.StdEncoding.EncodeToString(data),
	}, nil
}

func mimeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

func buildTools(defs []openai.ToolDefinition) ([]antigravity.ToolGroup, int) {
	if len(defs) == 0 {
		return nil, 0
	}
	decls := make([]antigravity.FunctionDeclaration, 0, len(defs))
	for _, d := range defs {
		if d.Function.Name == "" {
			continue
		}
		decl := antigravity.FunctionDeclaration{
			Name:        d.Function.Name,
			Description: d.Function.Description,
		}
		if params, ok := d.Function.Parameters.(map[string]any); ok {
			decl.ParametersJSONSchema = CleanSchema(params, params)
		} else if d.Function.Parameters != nil {
			// Best effort: round-trip through JSON so exotic types still work.
			if raw, err := json.Marshal(d.Function.Parameters); err == nil {
				var generic map[string]any
				if json.Unmarshal(raw, &generic) == nil {
					decl.ParametersJSONSchema = CleanSchema(generic, generic)
				}
			}
		}
		decls = append(decls, decl)
	}
	if len(decls) == 0 {
		return nil, 0
	}
	return []antigravity.ToolGroup{{FunctionDeclarations: decls}}, len(decls)
}

func buildGenerationConfig(req openai.ChatCompletionRequest, profile ModelProfile) *antigravity.GenerationConfig {
	cfg := &antigravity.GenerationConfig{}
	if req.Temperature != nil {
		cfg.Temperature = req.Temperature
	}
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		cfg.MaxOutputTokens = req.MaxTokens
	}
	if profile.ThinkingLevel != "" {
		cfg.ThinkingConfig = &antigravity.ThinkingConfig{
			ThinkingLevel:   profile.ThinkingLevel,
			IncludeThoughts: profile.IncludeThoughts,
		}
	}
	if cfg.Temperature == nil && cfg.MaxOutputTokens == nil && cfg.ThinkingConfig == nil {
		return nil
	}
	return cfg
}
