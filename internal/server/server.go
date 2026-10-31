package server

import (
	"net"
	"net/http"
	"strconv"
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
	mux.Handle("POST /v1/chat/completions", mw.LimitConcurrency(handleChatNative(cfg)))

	return mw.CORS(mux)
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
