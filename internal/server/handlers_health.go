package server

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/yaping-pro/agyapi/internal/config"
)

// handleHealth reports readiness for GET /health. With a single upstream
// strategy, readiness is the token file: without it every request fails
// upstream, and nothing else needs probing.
func handleHealth(cfg *config.Config, mw *Middleware) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		active := int64(0)
		if mw != nil {
			active = mw.ActiveRequests()
		}

		defaultModel := ""
		maxConcurrency := 4
		tokenReady := false

		if cfg != nil {
			defaultModel = cfg.DefaultModel
			maxConcurrency = cfg.MaxConcurrency
			if cfg.TokenPath != "" {
				if _, err := os.Stat(cfg.TokenPath); err == nil {
					tokenReady = true
				}
			}
		}

		resp := map[string]any{
			"status":          "ok",
			"version":         Version,
			"token_ready":     tokenReady,
			"default_model":   defaultModel,
			"active_requests": active,
			"max_concurrency": maxConcurrency,
		}

		_ = json.NewEncoder(w).Encode(resp)
	}
}
