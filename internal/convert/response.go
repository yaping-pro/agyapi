package convert

import (
	"encoding/json"
	"strings"

	"github.com/yaping-pro/agyapi/internal/antigravity"
	"github.com/yaping-pro/agyapi/internal/openai"
)

// ChunkMeta is the OpenAI-facing identity attached to every emitted chunk.
type ChunkMeta struct {
	ID      string
	Created int64
	Model   string
}

// ConvertChunk turns one decoded upstream chunk into OpenAI stream chunks.
//
// It returns the terminal finish_reason ("" when the turn is still open) and
// real usage accounting when the upstream reports it.
func ConvertChunk(meta ChunkMeta, resp *antigravity.GenerateResponse) ([]openai.ChatCompletionChunk, string, *openai.UsageInfo) {
	if resp == nil {
		return nil, "", nil
	}

	var chunks []openai.ChatCompletionChunk
	sawToolCall := false
	toolIndex := 0

	for _, cand := range resp.Candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				// Text and a function call may share one part, so handle them
				// independently rather than mutually exclusively.
				if part.Text != "" {
					if part.Thought {
						chunks = append(chunks, newChunk(meta, openai.ChunkDelta{ReasoningContent: part.Text}))
					} else {
						chunks = append(chunks, newChunk(meta, openai.ChunkDelta{Content: part.Text}))
					}
				}

				if part.FunctionCall != nil {
					tc := openai.ToolCall{
						Index: intPtr(toolIndex),
						ID:    EncodeToolID(part.FunctionCall.ID, part.ThoughtSignature),
						Type:  "function",
						Function: openai.FunctionCall{
							Name:      part.FunctionCall.Name,
							Arguments: normalizeArgs(part.FunctionCall.Args),
						},
					}
					toolIndex++
					sawToolCall = true
					chunks = append(chunks, newChunk(meta, openai.ChunkDelta{ToolCalls: []openai.ToolCall{tc}}))
				}
			}
		}
	}

	var usage *openai.UsageInfo
	if resp.UsageMetadata != nil {
		u := resp.UsageMetadata
		total := u.TotalTokenCount
		if total == 0 {
			total = u.PromptTokenCount + u.CandidatesTokenCount
		}
		usage = &openai.UsageInfo{
			PromptTokens:     u.PromptTokenCount,
			CompletionTokens: u.CandidatesTokenCount,
			TotalTokens:      total,
		}
	}

	finish := ""
	if sawToolCall {
		finish = "tool_calls"
	} else if resp.Candidates != nil {
		for _, cand := range resp.Candidates {
			if cand.FinishReason != "" {
				finish = mapFinishReason(cand.FinishReason)
				break
			}
		}
	}

	// Usage is emitted by the HTTP layer on the terminal chunk, so it is
	// returned here rather than duplicated onto a content chunk.
	return chunks, finish, usage
}

func newChunk(meta ChunkMeta, delta openai.ChunkDelta) openai.ChatCompletionChunk {
	return openai.ChatCompletionChunk{
		ID:      meta.ID,
		Object:  "chat.completion.chunk",
		Created: meta.Created,
		Model:   meta.Model,
		Choices: []openai.ChunkChoice{{Index: 0, Delta: delta}},
	}
}

// mapFinishReason translates upstream reasons onto the OpenAI vocabulary.
func mapFinishReason(reason string) string {
	switch strings.ToUpper(reason) {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		return "content_filter"
	default:
		return "stop"
	}
}

func normalizeArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "{}"
	}
	if !json.Valid([]byte(trimmed)) {
		return "{}"
	}
	return trimmed
}

func intPtr(v int) *int { return &v }

// BuildNonStreamResponse assembles a regular chat.completion object from the
// accumulated stream state.
func BuildNonStreamResponse(meta ChunkMeta, text, reasoning string, toolCalls []openai.ToolCall, finish string, usage openai.UsageInfo) openai.ChatCompletionResponse {
	if finish == "" {
		finish = "stop"
	}
	msg := openai.ChatMessage{
		Role:    "assistant",
		Content: text,
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	return openai.ChatCompletionResponse{
		ID:      meta.ID,
		Object:  "chat.completion",
		Created: meta.Created,
		Model:   meta.Model,
		Choices: []openai.ChatChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: finish,
		}},
		Usage: usage,
	}
}
