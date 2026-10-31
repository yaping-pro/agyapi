package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yaping-pro/agyapi/internal/config"
	"github.com/yaping-pro/agyapi/internal/openai"
	"github.com/yaping-pro/agyapi/internal/print/bridge"
	"github.com/yaping-pro/agyapi/internal/print/prompt"
)

var keepAliveInterval = 15 * time.Second

func generateChatID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	return "chatcmpl-" + hex.EncodeToString(b)
}

func resolveModel(reqModel string, defaultModel string) string {
	fallback := defaultModel
	if fallback == "" {
		fallback = "gemini-3.8-flash-high"
	}
	if reqModel == "" || reqModel == "default" || reqModel == "gpt-4o" || reqModel == "gpt-4" {
		return fallback
	}
	if strings.HasPrefix(reqModel, "-") {
		slog.Warn("suspicious model name with leading hyphen rejected, falling back to default", "model", reqModel)
		return fallback
	}
	if mapped, ok := modelAliases[reqModel]; ok {
		return mapped
	}
	if !isKnownModel(reqModel) && reqModel != fallback {
		slog.Info("unknown model requested, forwarding directly to agy", "model", reqModel)
	}
	return reqModel
}

// holdBackTagPrefix splits s into (safeToFlush, heldSuffix) where heldSuffix is any
// trailing suffix of s that is a prefix of "<tool_call>".
func holdBackTagPrefix(s string) (string, string) {
	const tag = "<tool_call>"
	maxCheck := len(tag) - 1
	if len(s) < maxCheck {
		maxCheck = len(s)
	}
	for k := maxCheck; k >= 1; k-- {
		suffix := s[len(s)-k:]
		if strings.HasPrefix(tag, suffix) {
			return s[:len(s)-k], suffix
		}
	}
	return s, ""
}

// handleChatCompletions returns an HTTP handler for POST /v1/chat/completions supporting
// both streaming SSE and non-streaming execution against the agy CLI bridge.
func handleChatCompletions(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()
		r.Body = http.MaxBytesReader(w, r.Body, 16<<20)

		var req openai.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(openai.ErrorResponse{
				Error: openai.APIError{
					Message: fmt.Sprintf("invalid JSON payload: %v", err),
					Type:    "invalid_request_error",
				},
			})
			return
		}

		if len(req.Messages) == 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(openai.ErrorResponse{
				Error: openai.APIError{
					Message: "messages array cannot be empty",
					Type:    "invalid_request_error",
				},
			})
			return
		}

		model := resolveModel(req.Model, cfg.DefaultModel)
		cleanupImages, imgErr := openai.MaterializeImages(req.Messages)
		if imgErr != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(openai.ErrorResponse{
				Error: openai.APIError{
					Message: "failed to process images: " + imgErr.Error(),
					Type:    "invalid_request_error",
				},
			})
			return
		}
		defer cleanupImages()
		compiledPrompt := prompt.CompilePrompt(req.Messages, req.Tools)
		chatId := generateChatID()
		createdTime := time.Now().Unix()

		if req.Stream {
			flusher, ok := w.(http.Flusher)
			if !ok {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"message": "Streaming unsupported",
						"type":    "server_error",
					},
				})
				return
			}

			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.WriteHeader(http.StatusOK)
			flusher.Flush()

			var writeMu sync.Mutex
			lastActivity := time.Now()
			completed := false

			// Send initial role chunk
			initialChunk := openai.ChatCompletionChunk{
				ID:      chatId,
				Object:  "chat.completion.chunk",
				Created: createdTime,
				Model:   model,
				Choices: []openai.ChunkChoice{
					{
						Index: 0,
						Delta: openai.ChunkDelta{Role: "assistant", Content: ""},
					},
				},
			}
			initBytes, _ := json.Marshal(initialChunk)
			writeMu.Lock()
			if _, err := fmt.Fprintf(w, "data: %s\n\n", initBytes); err != nil {
				writeMu.Unlock()
				return
			}
			flusher.Flush()
			lastActivity = time.Now()
			writeMu.Unlock()

			interval := keepAliveInterval
			ticker := time.NewTicker(interval)
			doneChan := make(chan struct{})
			defer func() {
				ticker.Stop()
				close(doneChan)
			}()

			go func() {
				for {
					select {
					case <-doneChan:
						return
					case <-r.Context().Done():
						return
					case now := <-ticker.C:
						writeMu.Lock()
						if completed {
							writeMu.Unlock()
							return
						}
						if now.Sub(lastActivity) >= interval {
							fmt.Fprintf(w, ": keep-alive\n\n")
							flusher.Flush()
							lastActivity = now
						}
						writeMu.Unlock()
					}
				}
			}()

			var (
				fullBuf    strings.Builder
				pending    string
				inToolCall bool
			)

			sendContentDelta := func(text string) error {
				if text == "" {
					return nil
				}
				chunk := openai.ChatCompletionChunk{
					ID:      chatId,
					Object:  "chat.completion.chunk",
					Created: createdTime,
					Model:   model,
					Choices: []openai.ChunkChoice{
						{
							Index: 0,
							Delta: openai.ChunkDelta{Content: text},
						},
					},
				}
				b, _ := json.Marshal(chunk)
				writeMu.Lock()
				defer writeMu.Unlock()
				if completed {
					return nil
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
					return err
				}
				flusher.Flush()
				lastActivity = time.Now()
				return nil
			}

			onDelta := func(delta string) error {
				fullBuf.WriteString(delta)
				if inToolCall {
					return nil
				}
				pending += delta
				if idx := strings.Index(pending, "<tool_call>"); idx != -1 {
					before := pending[:idx]
					pending = ""
					inToolCall = true
					return sendContentDelta(before)
				}
				safe, held := holdBackTagPrefix(pending)
				pending = held
				return sendContentDelta(safe)
			}

			finalizeStream := func(res *bridge.ResultInfo) error {
				fullText := fullBuf.String()
				if fullText == "" && res != nil && res.Response != "" {
					fullText = res.Response
				}
				_, toolCalls := prompt.ExtractHermesToolCalls(fullText)
				if !inToolCall && pending != "" {
					if err := sendContentDelta(pending); err != nil {
						return err
					}
					pending = ""
				}

				stop := "stop"
				if len(toolCalls) > 0 {
					stop = "tool_calls"
					for i := range toolCalls {
						idx := i
						toolCalls[i].Index = &idx
					}
					tcChunk := openai.ChatCompletionChunk{
						ID:      chatId,
						Object:  "chat.completion.chunk",
						Created: createdTime,
						Model:   model,
						Choices: []openai.ChunkChoice{
							{
								Index: 0,
								Delta: openai.ChunkDelta{ToolCalls: toolCalls},
							},
						},
					}
					tb, _ := json.Marshal(tcChunk)
					writeMu.Lock()
					if !completed {
						_, _ = fmt.Fprintf(w, "data: %s\n\n", tb)
						flusher.Flush()
						lastActivity = time.Now()
					}
					writeMu.Unlock()
				}

				finalChunk := openai.ChatCompletionChunk{
					ID:      chatId,
					Object:  "chat.completion.chunk",
					Created: createdTime,
					Model:   model,
					Choices: []openai.ChunkChoice{
						{
							Index:        0,
							Delta:        openai.ChunkDelta{},
							FinishReason: &stop,
						},
					},
				}
				b, _ := json.Marshal(finalChunk)
				writeMu.Lock()
				defer writeMu.Unlock()
				if completed {
					return nil
				}
				completed = true
				if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(w, "data: [DONE]\n\n"); err != nil {
					return err
				}
				flusher.Flush()
				lastActivity = time.Now()

				if res != nil {
					slog.Info("chat completion stream completed",
						"model", model,
						"duration", time.Since(startTime),
						"tool_calls", len(toolCalls),
						"total_tokens", res.Usage.TotalTokens,
						"prompt_tokens", res.Usage.InputTokens,
						"completion_tokens", res.Usage.OutputTokens,
					)
				} else {
					slog.Info("chat completion stream completed",
						"model", model,
						"duration", time.Since(startTime),
						"tool_calls", len(toolCalls),
					)
				}
				return nil
			}

			onDone := func(res *bridge.ResultInfo) error {
				if res.Status != "" && res.Status != "SUCCESS" {
					return fmt.Errorf("agy returned error status %q: %s", res.Status, res.Response)
				}
				return finalizeStream(res)
			}

			err := bridge.RunAgyStream(r.Context(), cfg, compiledPrompt, model, onDelta, onDone)
			writeMu.Lock()
			alreadyDone := completed
			writeMu.Unlock()
			if err != nil && !alreadyDone && r.Context().Err() == nil {
				writeMu.Lock()
				completed = true
				slog.Error("error during agy stream", "error", err)
				errPayload, _ := json.Marshal(map[string]any{
					"error": map[string]any{
						"message": err.Error(),
						"type":    "backend_error",
					},
				})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", errPayload)
				flusher.Flush()
				writeMu.Unlock()
			} else if !alreadyDone && err == nil && r.Context().Err() == nil {
				_ = finalizeStream(nil)
			}
			return
		}

		// Non-streaming execution
		res, err := bridge.RunAgyNonStream(r.Context(), cfg, compiledPrompt, model)
		if err != nil {
			slog.Error("agy non-stream error", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": err.Error(),
					"type":    "backend_error",
				},
			})
			return
		}

		cleanedContent, toolCalls := prompt.ExtractHermesToolCalls(res.Response)
		finishReason := "stop"
		if len(toolCalls) > 0 {
			finishReason = "tool_calls"
		}

		totalTokens := res.Usage.TotalTokens
		if totalTokens == 0 {
			totalTokens = res.Usage.InputTokens + res.Usage.OutputTokens
		}

		slog.Info("chat completion completed",
			"model", model,
			"duration", time.Since(startTime),
			"total_tokens", totalTokens,
			"prompt_tokens", res.Usage.InputTokens,
			"completion_tokens", res.Usage.OutputTokens,
		)

		resp := openai.ChatCompletionResponse{
			ID:      chatId,
			Object:  "chat.completion",
			Created: createdTime,
			Model:   model,
			Choices: []openai.ChatChoice{
				{
					Index: 0,
					Message: openai.ChatMessage{
						Role:      "assistant",
						Content:   cleanedContent,
						ToolCalls: toolCalls,
					},
					FinishReason: finishReason,
				},
			},
			Usage: openai.UsageInfo{
				PromptTokens:     res.Usage.InputTokens,
				CompletionTokens: res.Usage.OutputTokens,
				TotalTokens:      totalTokens,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
