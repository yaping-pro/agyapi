# Architecture

## The problem this solves

An Antigravity account can be perfectly valid while the vendor CLI refuses to
run it, because the eligibility check lives in the **client**:

```text
error: Eligibility check failed: Your current account is not eligible for
Antigravity, because it is not currently available in your location.
```

Probing the same account directly gets a different answer:

```json
POST /v1internal:loadCodeAssist
{
  "currentTier": { "id": "standard-tier", "name": "Antigravity" },
  "cloudaicompanionProject": "aicode-consumers",
  "ineligibleTiers": [ { "tierId": "free-tier", "reasonCode": "UNSUPPORTED_LOCATION" } ]
}
```

`currentTier` is granted; the location reason hangs off the unused `free-tier`
entry. A request to `v1internal:streamGenerateContent` carrying that account's
OAuth token returns `200` and real completions.

So the region gate is **cosmetic at the transport layer**. `agyapi` consumes the
credential directly over HTTPS and never asks the vendor CLI whether it is
allowed to run. That is why there is no binary patching, no wrapper, and no
subprocess anywhere in this repository.

## One path, end to end

```text
                            ┌──────────────────────────────────────┐
  OpenAI-compatible client  │  agyapi (:7862)                      │
  (omp, Cursor, SDK, …)     │                                      │
        │                   │  internal/server   HTTP + SSE        │
        └── POST /v1/chat ─►│  internal/openai   wire types        │
            /completions    │  internal/convert  translation       │
                            │  internal/auth     token file        │
                            │  internal/antigravity  upstream I/O  │
                            └───────────────┬──────────────────────┘
                                            │ HTTPS
                                            ▼
                        daily-cloudcode-pa.googleapis.com
                        /v1internal:streamGenerateContent?alt=sse
```

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
     text         → delta.content
     thought      → delta.reasoning_content
     functionCall → delta.tool_calls (id carries thoughtSignature)
     usageMetadata → usage
6. server.sseSink
     terminal chunk with finish_reason + usage, then data: [DONE]
```

## Design decisions worth stating

**No subprocess.** The obvious implementation drives the vendor CLI (pipe a
compiled prompt to stdin, parse `<tool_call>` XML back). It was tried and
removed; the four failures below are all direct consequences of that shape, and
none of them are fixable from outside it:

| Symptom | Cause |
| :--- | :--- |
| long conversations silently lose the latest instruction | CLI caps one input step at 16,384 tokens |
| the caller's tool loop is bypassed | the CLI has its own `run_command` and will use it |
| usage is always reported as 0 | the CLI's text output carries no token counts |
| macOS dialog `A keychain cannot be found to store "antigravity"` | the CLI tries Keychain before file storage |

Structured HTTP has none of these properties, and costs less code.

**The translation layer is the product.** `internal/convert` is where an OpenAI
request becomes a valid Gemini request: schema sanitizing (`$ref`, `allOf`,
`const` unions, nullable types), history ordering, tool pairing, and the
`thoughtSignature` round-trip. Everything else is plumbing.

**`thoughtSignature` inside `tool_call_id`.** Upstream expects each tool turn to
replay the signature it issued. OpenAI clients drop unknown fields but preserve
ids verbatim, so the gateway appends `__thought__<signature>` to the id and
splits on the first occurrence going back out.

**Stateless.** Nothing is persisted but the credential. No conversation store, no
prompt cache beyond `sessionId` affinity for upstream caching. Restarts are free.

**Always re-read the token file.** Caching it in memory either serves a revoked
token or clobbers a peer process's rotated refresh token. Google rotates the
refresh token on every refresh, so the store re-reads before and after, and
adopts a peer's state when it lost the race.

## Data ownership

| State | Owner | Unit |
| :--- | :--- | :--- |
| OAuth credential | `internal/auth.Store` | one JSON file, `0600`, atomic rename |
| Conversation | the caller | stateless per request |
| Upstream session | `sessionId` derived from the first user message | stable across turns |

## Testing strategy

- `internal/convert` and `internal/auth` hold the behavior that fails silently,
  so they are covered directly (schema sanitizing, signature round-trips, token
  rotation and peer adoption).
- `internal/server` runs the real handler against an `httptest` upstream that
  records the outgoing envelope, which is how the tool-name and
  developer-message invariants are pinned.
- Nothing in the unit suite touches the network.