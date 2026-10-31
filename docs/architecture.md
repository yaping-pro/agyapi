# Architecture

## The problem this solves

An Antigravity account can be perfectly valid while the vendor CLI refuses to
run it, because the eligibility check lives in the **client**:

```text
error: Eligibility check failed: Your current account is not eligible for
Antigravity, because it is not currently available in your location.
```

The backend, however, keeps serving that account. Verified by direct probe:

```json
POST /v1internal:loadCodeAssist
{
  "currentTier": { "id": "standard-tier", "name": "Antigravity" },
  "cloudaicompanionProject": "aicode-consumers",
  "ineligibleTiers": [ { "tierId": "free-tier", "reasonCode": "UNSUPPORTED_LOCATION" } ]
}
```

`currentTier` is granted; only the `free-tier` entry carries the location
reason. Requests to `v1internal:streamGenerateContent` with that account's OAuth
token return `200` and real completions.

So the region gate is **cosmetic at the transport layer**. `agyapi` consumes the
credential directly over HTTPS and never asks the vendor CLI whether it is
allowed to run. That is why the native backend needs no binary patching, no
wrapper, and no subprocess at all.

## Two backends

```text
                            ┌──────────────────────────────────────┐
  OpenAI-compatible client  │  agyapi (:7862)                      │
  (omp, Cursor, SDK, …)     │                                      │
        │                   │  internal/server   HTTP + SSE        │
        └── POST /v1/chat ─►│  internal/openai   wire types        │
            /completions    │  internal/convert  translation       │
                            │  internal/auth     token file        │
                            └───────────────┬──────────────────────┘
                                            │
                 ┌──────────────────────────┴───────────────────────────┐
                 │                                                      │
       backend=native (default)                            backend=print (legacy)
                 │                                                      │
       internal/antigravity                                  internal/print/bridge
                 │                                                      │
   HTTPS → daily-cloudcode-pa.googleapis.com                 subprocess → agy CLI
   /v1internal:streamGenerateContent?alt=sse                 (stdin: one text prompt)
                 │                                                      │
                 └──────────────► Google Cloud Code Assist ◄─────────────┘
```

### `native` — default

One HTTP request per turn. The conversation is a structured `contents[]` array;
tools are `functionDeclarations`; the response is parsed from SSE chunks.

Properties that matter:

- **No prompt budget.** The vendor CLI truncates a single input step at
  `max-tokens-per-step: 16384`, which silently amputates the tail of long
  conversations — observed at a 279 KB prompt, where the user's most recent
  instruction never reached the model. Direct HTTP has no such step.
- **No competing tool loop.** The CLI exposes its own `run_command`; with
  `permissions.allow: ["command(*)"]` it would happily execute the user's task
  itself, invisibly, while the caller sees a plain-text answer and an
  untouched todo list. With structured declarations there is exactly one tool
  executor: the caller.
- **Correct usage accounting.** `usageMetadata` is relayed, so clients that size
  compaction windows from reported tokens stop believing every request cost 0.
- **No Keychain.** The vendor CLI stores the token in the macOS Keychain when it
  can; with an isolated `HOME` it raises
  `A keychain cannot be found to store "antigravity"` and stalls for five
  seconds before falling back to a file. The gateway only ever touches the file.

### `print` — legacy

Compiles the whole conversation into one prompt, pipes it to `agy`, and parses
`<tool_call>` XML back out. It exists as a fallback if the HTTP contract changes.
Every failure mode listed above belongs to it.

## Request path (native)

```text
1. server.handleChatNative
     decode JSON, cap body at 16 MiB, materialize inline images
2. convert.BuildRequest
     system messages → systemInstruction
     assistant/user/tool/developer → contents[] in original order
     tools[] → functionDeclarations + toolConfig.mode=VALIDATED
     model selector → upstream id + thinkingLevel
3. auth.Store.AccessToken
     read token file → refresh in-process if near expiry → persist atomically
4. antigravity.Client.StreamGenerate
     POST {base}/v1internal:streamGenerateContent?alt=sse
     headers: Authorization, requestType, User-Agent
5. antigravity.SSE → convert.ConvertChunk
     text       → delta.content
     thought    → delta.reasoning_content
     functionCall → delta.tool_calls (id carries thoughtSignature)
     usageMetadata → usage
6. server.sseSink
     terminal chunk with finish_reason + usage, then data: [DONE]
```

## Data ownership

| State | Owner | Unit |
| :--- | :--- | :--- |
| OAuth credential | `internal/auth.Store` | one JSON file, `0600`, atomic rename |
| Conversation | the caller | stateless per request; the gateway keeps no history |
| Upstream session | `sessionId` derived from the first user message | stable across turns for prompt-cache affinity |

The gateway is stateless by design: nothing is persisted except the credential.
That makes restarts free and removes a whole class of "the proxy remembered
something the client forgot" bugs.

## Testing strategy

- `internal/convert` and `internal/auth` hold the behavior that fails silently,
  so they are covered directly (schema sanitizing, signature round-trips, token
  rotation and peer adoption).
- `internal/server` runs the real handler against an `httptest` upstream that
  records the outgoing envelope, which is how the tool-name and
  developer-message invariants are pinned.
- Nothing in the unit suite touches the network.