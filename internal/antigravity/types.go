package antigravity

import "encoding/json"

// GenerateRequest is the v1internal envelope the Antigravity backend expects.
// It mirrors what the official CLI posts to /v1internal:streamGenerateContent.
type GenerateRequest struct {
	Project     string        `json:"project"`
	RequestID   string        `json:"requestId"`
	Request     GeminiRequest `json:"request"`
	Model       string        `json:"model"`
	RequestType string        `json:"requestType"`
}

// GeminiRequest is the inner generateContent body.
type GeminiRequest struct {
	Contents          []Content         `json:"contents"`
	SystemInstruction *Content          `json:"systemInstruction,omitempty"`
	Tools             []ToolGroup       `json:"tools,omitempty"`
	ToolConfig        *ToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
	SessionID         string            `json:"sessionId,omitempty"`
}

// Content is a single conversation turn.
type Content struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

// Part is one piece of a turn: plain text, hidden reasoning, a tool call,
// a tool result, or inline media.
type Part struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *InlineData       `json:"inlineData,omitempty"`
}

// FunctionCall is a model-issued tool invocation.
type FunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
	ID   string          `json:"id,omitempty"`
}

// FunctionResponse carries a tool result back to the model.
type FunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
	ID       string          `json:"id,omitempty"`
}

// InlineData embeds base64 media.
type InlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// ToolGroup wraps function declarations the way Gemini expects.
type ToolGroup struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations"`
}

// FunctionDeclaration describes one callable tool.
type FunctionDeclaration struct {
	Name                 string         `json:"name"`
	Description          string         `json:"description,omitempty"`
	ParametersJSONSchema map[string]any `json:"parametersJsonSchema,omitempty"`
}

// ToolConfig controls how tool calling is enforced.
type ToolConfig struct {
	FunctionCallingConfig FunctionCallingConfig `json:"functionCallingConfig"`
}

// FunctionCallingConfig selects the tool-calling mode.
// VALIDATED keeps normal model agency; ANY forces a call from
// AllowedFunctionNames (used by the emit_answer anti-truncation trick).
type FunctionCallingConfig struct {
	Mode                 string   `json:"mode"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// GenerationConfig mirrors sampling and reasoning knobs.
type GenerationConfig struct {
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"topP,omitempty"`
	MaxOutputTokens *int            `json:"maxOutputTokens,omitempty"`
	ThinkingConfig  *ThinkingConfig `json:"thinkingConfig,omitempty"`
}

// ThinkingConfig selects reasoning depth. For tiered models the depth is
// encoded in the model id upstream, so only ThinkingLevel is relevant.
type ThinkingConfig struct {
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
}

// StreamEnvelope wraps each SSE payload from the upstream stream.
type StreamEnvelope struct {
	Response *GenerateResponse `json:"response"`
	TraceID  string            `json:"traceId"`
}

// GenerateResponse is one decoded chunk.
type GenerateResponse struct {
	Candidates    []Candidate    `json:"candidates"`
	UsageMetadata *UsageMetadata `json:"usageMetadata"`
	ModelVersion  string         `json:"modelVersion"`
	ResponseID    string         `json:"responseId"`
}

// Candidate holds one completion alternative.
type Candidate struct {
	Content      *Content `json:"content"`
	FinishReason string   `json:"finishReason,omitempty"`
}

// UsageMetadata reports real token accounting.
type UsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
}

// LoadCodeAssistRequest is the onboarding probe body.
type LoadCodeAssistRequest struct {
	Metadata Metadata `json:"metadata"`
}

// Metadata identifies the client to Google.
type Metadata struct {
	IDEType    string `json:"ideType,omitempty"`
	Platform   string `json:"platform,omitempty"`
	PluginType string `json:"pluginType,omitempty"`
}

// LoadCodeAssistResponse carries the resolved project and tier info.
type LoadCodeAssistResponse struct {
	CurrentTier             *Tier  `json:"currentTier"`
	CloudAICompanionProject string `json:"cloudaicompanionProject"`
	AllowedTiers            []Tier `json:"allowedTiers"`
	IneligibleTiers         []Tier `json:"ineligibleTiers"`
}

// Tier describes an entitlement level.
type Tier struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ReasonCode    string `json:"reasonCode,omitempty"`
	ReasonMessage string `json:"reasonMessage,omitempty"`
	TierID        string `json:"tierId,omitempty"`
}
