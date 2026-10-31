package bridge

// AgyJSONResponse represents the non-streaming JSON output from the agy CLI.
type AgyJSONResponse struct {
	ConversationID  string   `json:"conversation_id"`
	Status          string   `json:"status"`
	Response        string   `json:"response"`
	DurationSeconds float64  `json:"duration_seconds"`
	Usage           AgyUsage `json:"usage"`
}

// AgyUsage tracks token usage reported by the agy CLI.
type AgyUsage struct {
	InputTokens    int `json:"input_tokens"`
	OutputTokens   int `json:"output_tokens"`
	ThinkingTokens int `json:"thinking_tokens"`
	TotalTokens    int `json:"total_tokens"`
}

// StreamEvent represents a single event line in the NDJSON stream from agy.
type StreamEvent struct {
	Event      string          `json:"event"`
	StepUpdate *StepUpdateInfo `json:"step_update,omitempty"`
	Result     *ResultInfo     `json:"result,omitempty"`
}

// StepUpdateInfo contains the incremental state and text delta for a stream step update.
type StepUpdateInfo struct {
	ConversationID string `json:"conversation_id,omitempty"`
	StepIndex      int    `json:"step_index,omitempty"`
	State          string `json:"state"`
	TextDelta      string `json:"text_delta"`
}

// ResultInfo contains the final execution result and token usage for a stream session.
type ResultInfo struct {
	ConversationID string   `json:"conversation_id,omitempty"`
	Status         string   `json:"status"`
	Response       string   `json:"response"`
	Usage          AgyUsage `json:"usage"`
}
