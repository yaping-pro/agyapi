package server

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yaping-pro/agyapi/internal/config"
)

// NewHandler builds the full route table plus middleware. It is separated from
// NewServer so tests can serve it with httptest without binding a port.
func NewHandler(cfg *config.Config) http.Handler {
	mux := http.NewServeMux()
	mw := NewMiddleware(cfg.MaxConcurrency)

	mux.HandleFunc("GET /health", handleHealth(cfg, mw))
	mux.HandleFunc("GET /v1/models", handleModels(cfg))
	mux.HandleFunc("GET /v1/models/", handleModelDetail(cfg))
	mux.HandleFunc("GET /v1/models/{model}", handleModelDetail(cfg))
	mux.Handle("POST /v1/chat/completions", mw.LimitConcurrency(chatHandler(cfg)))

	return mw.CORS(mux)
}

// chatHandler selects the upstream strategy. An empty Backend keeps the legacy
// print-mode bridge so existing deployments and tests stay valid.
func chatHandler(cfg *config.Config) http.Handler {
	if strings.EqualFold(cfg.Backend, "native") {
		return handleChatNative(cfg)
	}
	return handleChatCompletions(cfg)
}

// NewServer constructs and configures the HTTP server for agyapi.
// It wires the route table and configures timeouts suited for long-running SSE
// streams.
func NewServer(cfg *config.Config) *http.Server {
	return &http.Server{
		Addr:         net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Handler:      NewHandler(cfg),
		WriteTimeout: 0, // Disable write timeout for unbounded SSE streaming
		ReadTimeout:  30 * time.Second,
	}
}
