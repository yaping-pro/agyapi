# agyapi Design Document

**Author**: Antigravity Assistant  
**Date**: 2026-09-10  
**Status**: Revised & Approved  

---

## 1. Objective

Create a lightweight, local API server in Go (`agyapi`) that exposes standard OpenAI-compatible endpoints (`/v1/chat/completions`, `/v1/models`, `/v1/models/{model}`, `/health`). The server translates incoming chat completion requests into non-interactive invocations of the local Antigravity CLI binary (`agy`), allowing external agent frameworks like Hermes Agent, Cursor, OpenHands, and Continue to use the local Antigravity subscription as an LLM backend without needing separate API keys.

---

## 2. Architecture & Tech Stack

### 2.1 Technology Stack
- **Language**: Go 1.24+ (standard library `net/http`, `os/exec`, `log/slog`, zero external heavy dependencies).
- **Binary**: Standalone static binary `agyapi`, with minimal memory footprint (~15 MB RAM) and sub-millisecond dispatch latency.
- **Backend Bridge**: Executes `~/.local/bin/agy` (or detected via PATH / `-agy-path` flag) passing prompts via `stdin` to eliminate Linux `E2BIG` (argument list too long) limits.
- **Process Group Isolation**: Spawns `agy` in an isolated process group (`Setpgid: true`) to ensure all child and helper processes cleanly terminate when an HTTP client aborts.

### 2.2 System Diagram

```
+--------------------------------------------------------------------+
| External Client (e.g. Hermes Agent, Cursor, OpenHands, Continue)   |
+--------------------------------------------------------------------+
                                 |
                                 | HTTP (OpenAI API format, CORS enabled)
                                 v
+--------------------------------------------------------------------+
| agyapi (Local Go Server on http://127.0.0.1:8000)                  |
|                                                                    |
|  - Middleware: CORS, Concurrency Limiter (semaphore), Logging      |
|  - Routes:                                                         |
|      GET  /health                                                  |
|      GET  /v1/models                                               |
|      GET  /v1/models/{model}                                       |
|      POST /v1/chat/completions (streaming SSE & non-streaming JSON)|
+--------------------------------------------------------------------+
                                 |
                                 | Stdin Pipe (unlimited size)
                                 | Stderr concurrent draining
                                 | Process Group signal control
                                 v
+--------------------------------------------------------------------+
| Antigravity CLI (~/.local/bin/agy)                                 |
| (Uses active Antigravity account & subscription)                   |
+--------------------------------------------------------------------+
```

---

## 3. Endpoints Specification

### 3.1 `GET /health`
Returns JSON status of the service:
```json
{
  "status": "ok",
  "version": "1.0.0",
  "agy_path": "~/.local/bin/agy",
  "default_model": "gemini-3.8-flash-high",
  "active_requests": 0,
  "max_concurrency": 4
}
```

### 3.2 `GET /v1/models` and `GET /v1/models/{model}`
Returns available models supported by the local `agy` CLI in standard OpenAI format with required `created` timestamp:
```json
{
  "object": "list",
  "data": [
    {"id": "gemini-3.8-flash-high", "object": "model", "created": 1725980000, "owned_by": "antigravity"},
    {"id": "gemini-3.8-flash-medium", "object": "model", "created": 1725980000, "owned_by": "antigravity"},
    {"id": "gemini-3.8-flash-low", "object": "model", "created": 1725980000, "owned_by": "antigravity"},
    {"id": "gemini-3.7-flash-high", "object": "model", "created": 1725980000, "owned_by": "antigravity"},
    {"id": "gemini-3.7-flash-medium", "object": "model", "created": 1725980000, "owned_by": "antigravity"},
    {"id": "gemini-3.7-flash-low", "object": "model", "created": 1725980000, "owned_by": "antigravity"},
    {"id": "gemini-3.1-pro-high", "object": "model", "created": 1725980000, "owned_by": "antigravity"},
    {"id": "gemini-3.1-pro-low", "object": "model", "created": 1725980000, "owned_by": "antigravity"},
    {"id": "claude-sonnet-4-6", "object": "model", "created": 1725980000, "owned_by": "antigravity"},
    {"id": "claude-opus-4-6-thinking", "object": "model", "created": 1725980000, "owned_by": "antigravity"},
    {"id": "gpt-oss-120b-medium", "object": "model", "created": 1725980000, "owned_by": "antigravity"}
  ]
}
```
*Note: Any unknown model or alias (e.g. `gpt-4o`, `default`, `claude-3-5-sonnet`) gracefully maps to the configured `-default-model`.*

### 3.3 `POST /v1/chat/completions`

Accepts standard OpenAI JSON request with flexible message content (both string and multi-part `[{"type":"text","text":"..."}]` arrays supported):
```json
{
  "model": "gemini-3.8-flash-high",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello!"}
  ],
  "stream": true,
  "temperature": 0.7,
  "max_tokens": 1024,
  "tools": [...]
}
```

#### A. Non-Streaming Response (`stream: false`)
Returns standard OpenAI chat completion JSON. If tools were called, `finish_reason` is `"tool_calls"`, otherwise `"stop"`:
```json
{
  "id": "chatcmpl-12345",
  "object": "chat.completion",
  "created": 1725980000,
  "model": "gemini-3.8-flash-high",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Hello! How can I assist you today?"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 120,
    "completion_tokens": 15,
    "total_tokens": 135
  }
}
```

When tool calls are extracted:
```json
{
  "id": "chatcmpl-12345",
  "object": "chat.completion",
  "created": 1725980000,
  "model": "gemini-3.8-flash-high",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Let me look up the weather for you.",
        "tool_calls": [
          {
            "id": "call_98765",
            "type": "function",
            "function": {
              "name": "get_weather",
              "arguments": "{\"location\":\"Paris\"}"
            }
          }
        ]
      },
      "finish_reason": "tool_calls"
    }
  ],
  "usage": {
    "prompt_tokens": 150,
    "completion_tokens": 30,
    "total_tokens": 180
  }
}
```

#### B. Streaming Response (`stream: true`)
Returns Server-Sent Events (`Content-Type: text/event-stream; charset=utf-8`) with `http.Flusher.Flush()` on every chunk:
```text
data: {"id":"chatcmpl-12345","object":"chat.completion.chunk","created":1725980000,"model":"gemini-3.8-flash-high","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-12345","object":"chat.completion.chunk","created":1725980000,"model":"gemini-3.8-flash-high","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-12345","object":"chat.completion.chunk","created":1725980000,"model":"gemini-3.8-flash-high","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}

data: {"id":"chatcmpl-12345","object":"chat.completion.chunk","created":1725980000,"model":"gemini-3.8-flash-high","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

#### C. Mid-Stream Error Handling
- If an error occurs **before** HTTP headers are sent: Returns HTTP 500 with standard OpenAI JSON error.
- If an error occurs **during** SSE streaming: Emits an SSE error frame before closing:
  ```text
  data: {"error":{"message":"backend process terminated","type":"backend_error","code":500}}

  data: [DONE]
  ```

---

## 4. Message & Tool Translation Engine

### 4.1 Message Formatting
Hermes Agent and standard OpenAI clients pass conversation history as an array of messages. `agyapi` compiles this array into a structured prompt delivered over `stdin`:
1. **Roles mapping**:
   - `system` / `developer`: Compiled into `[System Instructions]`.
   - `user`: Formatted as `User: <text>`.
   - `assistant`: If `tool_calls` are present, serializes `<tool_call>\n{"name":"...", "arguments": ...}\n</tool_call>`, followed by assistant text. Formatted as `Assistant: <text>`.
   - `tool`: Formatted as `Tool (<name>, id=<id>): <content>`.
2. **Tools schema injection**:
   If `tools` are present in the request, `agyapi` formats them as Hermes-compatible JSON tool schemas and appends instructions:
   ```text
   [Available Tools]
   <json list of tools>

   To call a tool, output exact XML blocks in the format:
   <tool_call>
   {"name": "<function_name>", "arguments": <args_dict>}
   </tool_call>
   ```
3. **Turn Ending**: Ends with `Assistant:` so the model directly begins generating the response.

### 4.2 Tool Call Extraction & Deduplication
- When parsing `agy`'s final response:
  - If `<tool_call>...</tool_call>` is found:
    - Extracts function name and arguments.
    - Serializes arguments to a JSON string.
    - Generates a unique `id` (e.g. `call_xyz`).
    - Sets `finish_reason: "tool_calls"`.
    - Strips `<tool_call>...</tool_call>` from `message.content` (leaving user-facing reasoning/preamble intact) to prevent double execution in agents that parse both text and structured tool calls.

---

## 5. Subprocess Execution & Process Safety

### 5.1 Stdin Piping (Preventing Linux `E2BIG`)
`agyapi` never passes large user prompts as command-line arguments (`-p`). Instead:
- `cmd := exec.CommandContext(ctx, cfg.AgyPath, args...)`
- `prompt` is written to `cmd.StdinPipe()` asynchronously in a goroutine.

### 5.2 Process Group Management (Preventing Zombie Processes)
```go
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
cmd.Cancel = func() error {
    // Send SIGTERM to entire process group (-PID)
    return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}
cmd.WaitDelay = 3 * time.Second // Escalate to SIGKILL if group doesn't exit
```

### 5.3 Stderr Concurrent Draining
To prevent the Linux 64 KB OS pipe buffer from filling and causing a deadlock on `stdout`:
- Stderr is drained asynchronously via an `io.Copy` into a thread-safe ring buffer (`bytes.Buffer` capped at 128 KB).
- If `agy` exits with an error, the drained stderr is included in the error log and response.

### 5.4 agy Output Format Specification
`agyapi` parses `agy` stdout according to its verified output schemas:
1. **Non-Streaming (`--output-format json`)**:
   ```json
   {
     "conversation_id": "string",
     "status": "SUCCESS",
     "response": "string response content",
     "duration_seconds": 1.23,
     "num_turns": 1,
     "usage": {
       "input_tokens": 1234,
       "output_tokens": 56,
       "thinking_tokens": 40,
       "total_tokens": 1290
     }
   }
   ```
2. **Streaming (`--output-format stream-json`)**:
   Emits NDJSON lines. `agyapi` scans lines with `bufio.Reader.ReadBytes('\n')`:
   - Text delta: `{"event":"step_update", "step_update":{"state":"ACTIVE"|"DONE", "text_delta":"chunk..."}}` -> emitted as SSE chunk.
   - Result: `{"event":"result", "result":{"status":"SUCCESS", "response":"...", "usage":{...}}}` -> emits final chunk with `finish_reason: "stop"` and `data: [DONE]`.

### 5.5 HTTP Server Timeouts & Keep-Alives
- HTTP server `WriteTimeout` is set to `0` to accommodate arbitrarily long reasoning streams.
- During streaming, if `agy` is generating thinking tokens or running internal processing without text output for >15 seconds, `agyapi` sends an SSE comment `: keep-alive\n\n` to prevent proxy/client socket disconnects.

### 5.6 Concurrency Limiter
- Uses a buffered channel semaphore (`chan struct{}`) bounded by `-max-concurrency` (default: `4`) to prevent overwhelming local resources or causing credential lock contention in `agy`.

---

## 6. Configuration & Usage

### 6.1 CLI Flags & Environment Variables
- `-port` (env `PORT`, default `8000`)
- `-host` (env `HOST`, default `127.0.0.1`)
- `-agy-path` (env `AGY_PATH`, default `~/.local/bin/agy`)
- `-default-model` (env `DEFAULT_MODEL`, default `gemini-3.8-flash-high`)
- `-max-concurrency` (env `MAX_CONCURRENCY`, default `4`)
- `-verbose` (env `VERBOSE`, default `false`)

### 6.2 Hermes Agent Integration
In Hermes Agent (or any external client):
- **Base URL**: `http://127.0.0.1:8000/v1`
- **API Key**: `local` (or any string)
- **Model**: `gemini-3.8-flash-high` (or `gemini-3.7-flash-high`, `claude-sonnet-4-6`, etc.)
