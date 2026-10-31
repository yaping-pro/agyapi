package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultUserAgent matches the Antigravity CLI so upstream accepts the call.
const DefaultUserAgent = "antigravity/1.4.10 darwin/arm64"

// TokenProvider yields a valid OAuth access token, refreshing when needed.
type TokenProvider interface {
	AccessToken(ctx context.Context) (string, error)
}

// APIError is a non-2xx response from the upstream API.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	msg := strings.TrimSpace(e.Body)
	if len(msg) > 400 {
		msg = msg[:400] + "..."
	}
	return fmt.Sprintf("upstream %d: %s", e.StatusCode, msg)
}

// Client talks to the Cloud Code Assist v1internal API directly, with no
// subprocess involved.
type Client struct {
	BaseURL   string
	Project   string
	Tokens    TokenProvider
	HTTP      *http.Client
	UserAgent string
}

// NewClient builds a client. A nil HTTP client gets a streaming-friendly
// default with no overall timeout (the request context governs lifetime).
func NewClient(baseURL, project string, tokens TokenProvider) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Project:   project,
		Tokens:    tokens,
		HTTP:      &http.Client{Transport: http.DefaultTransport},
		UserAgent: DefaultUserAgent,
	}
}

// StreamGenerate opens the SSE stream for a prepared request.
func (c *Client) StreamGenerate(ctx context.Context, req *GenerateRequest) (io.ReadCloser, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal upstream request: %w", err)
	}

	endpoint := c.BaseURL + "/v1internal:streamGenerateContent?alt=sse"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	token, err := c.tokens()
	if err != nil {
		return nil, err
	}

	c.setHeaders(httpReq, token)
	httpReq.Header.Set("Accept", "*/*")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(raw)}
	}
	return resp.Body, nil
}

// LoadProject asks the backend which cloudaicompanionProject to use.
func (c *Client) LoadProject(ctx context.Context) (string, error) {
	payload, err := json.Marshal(LoadCodeAssistRequest{
		Metadata: Metadata{IDEType: "ANTIGRAVITY", Platform: "DARWIN_ARM64", PluginType: "GEMINI"},
	})
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1internal:loadCodeAssist", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	token, err := c.tokens()
	if err != nil {
		return "", err
	}
	c.setHeaders(httpReq, token)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("loadCodeAssist failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return "", &APIError{StatusCode: resp.StatusCode, Body: string(raw)}
	}

	var out LoadCodeAssistResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode loadCodeAssist: %w", err)
	}
	if out.CloudAICompanionProject != "" {
		return out.CloudAICompanionProject, nil
	}
	if out.CurrentTier != nil && out.CurrentTier.ID != "" {
		return out.CurrentTier.ID, nil
	}
	return "", nil
}

func (c *Client) setHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("requestType", "agent")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
}

// tokens is a tiny adapter so Client can hold a nil provider gracefully.
func (c *Client) tokens() (string, error) {
	if c.Tokens == nil {
		return "", fmt.Errorf("no token provider configured")
	}
	return c.Tokens.AccessToken(context.Background())
}

// BuildSessionID derives a stable negative session id from the first user
// message, mirroring what the CLI does so upstream prompt caching can engage.
func BuildSessionID(seed string) string {
	if seed == "" {
		return fmt.Sprintf("-%d", time.Now().UnixNano())
	}
	var h uint64 = 1469598103934665603
	for i := range len(seed) {
		h ^= uint64(seed[i])
		h *= 1099511628211
	}
	return fmt.Sprintf("-%d", h&0x7FFFFFFFFFFFFFFF)
}

// RequestID returns a fresh request identifier.
func RequestID() string {
	return "req-" + url.QueryEscape(fmt.Sprintf("%d", time.Now().UnixNano()))
}
