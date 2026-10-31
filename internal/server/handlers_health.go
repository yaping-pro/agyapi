package server

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/yaping-pro/agyapi/internal/config"
)

// handleHealth returns an HTTP handler for GET /health that reports the service status,
// binary availability, default model, and real-time concurrency metrics.
func handleHealth(cfg *config.Config, mw *Middleware) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		active := int64(0)
		if mw != nil {
			active = mw.ActiveRequests()
		}

		agyPath := "agy"
		agyAvailable := false
		defaultModel := ""
		maxConcurrency := 4
		backend := "print"
		tokenReady := false

		if cfg != nil {
			if cfg.Backend != "" {
				backend = cfg.Backend
			}
			if cfg.AgyPath != "" {
				agyPath = filepath.Base(cfg.AgyPath)
				if _, err := os.Stat(cfg.AgyPath); err == nil {
					agyAvailable = true
				} else if _, err := exec.LookPath(cfg.AgyPath); err == nil {
					agyAvailable = true
				}
			}
			if backend == "native" {
				// In native mode the binary is irrelevant; readiness is the token file.
				agyAvailable = false
				if cfg.TokenPath != "" {
					if _, err := os.Stat(cfg.TokenPath); err == nil {
						tokenReady = true
					}
				}
			}
			defaultModel = cfg.DefaultModel
			maxConcurrency = cfg.MaxConcurrency
		}

		resp := map[string]any{
			"status":          "ok",
			"version":         "1.0.0",
			"backend":         backend,
			"token_ready":     tokenReady,
			"agy_available":   agyAvailable,
			"agy_path":        agyPath,
			"default_model":   defaultModel,
			"active_requests": active,
			"max_concurrency": maxConcurrency,
		}

		_ = json.NewEncoder(w).Encode(resp)
	}
}
