package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/yaping-pro/agyapi/internal/config"
	"github.com/yaping-pro/agyapi/internal/openai"
)

var knownModels = []string{
	"gemini-3.8-flash",
	"gemini-3.8-flash-high",
	"gemini-3.8-flash-medium",
	"gemini-3.8-flash-low",
	"gemini-4-argon",
	"gemini-4-argon-high",
	"gemini-4-argon-medium",
	"gemini-4-argon-low",
	"claude-sonnet-5-5",
	"claude-sonnet-5-5-low",
	"claude-sonnet-5-5-medium",
	"claude-sonnet-5-5-high",
	"claude-opus-5-5",
	"claude-opus-5-5-low",
	"claude-opus-5-5-medium",
	"claude-opus-5-5-high",
	"gpt-oss-120b",
	"gpt-oss-120b-medium",
	"claude-sonnet-4-6",
	"claude-opus-4-6",
	"claude-opus-4-6-thinking",
}

var modelAliases = map[string]string{
	"gemini-3.8-flash":         "gemini-3.8-flash-medium",
	"gemini-4-argon":           "gemini-4-argon-high",
	"claude-sonnet-5-5":        "claude-sonnet-5-5-medium",
	"claude-opus-5-5":          "claude-opus-5-5-medium",
	"gpt-oss-120b":             "gpt-oss-120b-medium",
	"claude-sonnet-4-6":        "claude-sonnet-5-5-medium",
	"claude-opus-4-6":          "claude-opus-5-5-high",
	"claude-opus-4-6-thinking": "claude-opus-5-5-high",
	"gemini-3.1-pro-high":      "gemini-4-argon-high",
	"gemini-3.1-pro-low":       "gemini-4-argon-low",
	"gemini-3.7-flash-high":    "gemini-3.8-flash-high",
	"gemini-3.7-flash-medium":  "gemini-3.8-flash-medium",
	"gemini-3.7-flash-low":     "gemini-3.8-flash-low",
}

func isKnownModel(model string) bool {
	for _, m := range knownModels {
		if m == model {
			return true
		}
	}
	return false
}

// handleModels returns an HTTP handler for GET /v1/models in standard OpenAI list format.
func handleModels(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		seen := make(map[string]bool)
		var models []openai.ModelObject

		addModel := func(id string) {
			if id != "" && !seen[id] {
				seen[id] = true
				models = append(models, openai.ModelObject{
					ID:      id,
					Object:  "model",
					Created: 1725980000,
					OwnedBy: "antigravity",
				})
			}
		}

		if cfg != nil && cfg.DefaultModel != "" {
			addModel(cfg.DefaultModel)
		}
		for _, m := range knownModels {
			addModel(m)
		}

		resp := openai.ModelListResponse{
			Object: "list",
			Data:   models,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// handleModelDetail returns an HTTP handler for GET /v1/models/{model} returning single model metadata.
func handleModelDetail(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		modelID := r.PathValue("model")
		if modelID == "" {
			modelID = strings.TrimPrefix(r.URL.Path, "/v1/models/")
		}
		modelID = strings.Trim(modelID, "/")

		if modelID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(openai.ErrorResponse{
				Error: openai.APIError{
					Message: "model id required",
					Type:    "invalid_request_error",
				},
			})
			return
		}

		isKnown := isKnownModel(modelID)
		if !isKnown && cfg != nil && cfg.DefaultModel == modelID {
			isKnown = true
		}
		if !isKnown {
			param := "model"
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(openai.ErrorResponse{
				Error: openai.APIError{
					Message: fmt.Sprintf("The model '%s' does not exist", modelID),
					Type:    "invalid_request_error",
					Param:   &param,
					Code:    "model_not_found",
				},
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(openai.ModelObject{
			ID:      modelID,
			Object:  "model",
			Created: 1725980000,
			OwnedBy: "antigravity",
		})
	}
}
