# Upstream contract notes

Everything here was established by probing a live account. It is documentation of
an undocumented protocol, so treat it as "observed", not "specified" — upstream
may change it without notice.

## Base URL

```text
https://daily-cloudcode-pa.googleapis.com
```

## Endpoints

| Endpoint | Method | Purpose |
| :--- | :--- | :--- |
| `/v1internal:streamGenerateContent?alt=sse` | POST | streaming completion |
| `/v1internal:generateContent` | POST | non-streaming completion |
| `/v1internal:loadCodeAssist` | POST | resolve project + tier |
| `/v1internal:fetchAvailableModels` | POST | model catalog |
| `/v1internal:retrieveUserQuotaSummary` | POST | quota |

## Request envelope

```json
{
  "project": "<cloudaicompanionProject>",
  "requestId": "req-<unique>",
  "requestType": "agent",
  "model": "gemini-3.8-flash-tiered",
  "request": {
    "contents": [
      { "role": "user",  "parts": [{ "text": "hi" }] },
      { "role": "model", "parts": [{ "text": "hello" }] }
    ],
    "systemInstruction": { "parts": [{ "text": "…" }] },
    "tools": [{ "functionDeclarations": [ { "name": "read", "parametersJsonSchema": { } } ] }],
    "toolConfig": { "functionCallingConfig": { "mode": "VALIDATED" } },
    "generationConfig": { "thinkingConfig": { "thinkingLevel": "HIGH", "includeThoughts": true } },
    "sessionId": "-<stable per conversation>"
  }
}
```

Headers:

```text
Authorization: Bearer <access_token>
Content-Type: application/json
requestType: agent          # also accepted as a body field
User-Agent: antigravity/<version> <os>/<arch>
```

`requestType` is `agent` for chat and `image_gen` for image models.

## Response envelope (SSE)

Each `data:` line is wrapped:

```json
{
  "response": {
    "candidates": [{
      "content": { "role": "model", "parts": [ … ] },
      "finishReason": "STOP"
    }],
    "usageMetadata": {
      "promptTokenCount": 1200,
      "candidatesTokenCount": 34,
      "totalTokenCount": 1234,
      "thoughtsTokenCount": 0,
      "cachedContentTokenCount": 0
    },
    "modelVersion": "gemini-3.8-flash-tiered",
    "responseId": "…"
  },
  "traceId": "…"
}
```

Part kinds seen in practice:

| part | meaning |
| :--- | :--- |
| `{"text": "…"}` | user-visible content |
| `{"text": "…", "thought": true}` | hidden reasoning; route to `reasoning_content` |
| `{"functionCall": {…}, "thoughtSignature": "…"}` | tool invocation |
| `{"inlineData": {…}}` | media |

Errors can arrive **in-band with HTTP 200**:

```json
{"error": {"code": 429, "message": "…", "status": "RESOURCE_EXHAUSTED"}}
```

The handler must check every payload for an `error` object before treating it as
a completion.

## `thoughtSignature`

Each tool-calling turn may carry an opaque `thoughtSignature`. It must be
replayed on the next request alongside the `functionCall`, or the upstream
rejects the turn.

OpenAI clients do not have a field for it and drop unknown properties, so the
gateway smuggles it inside the tool-call id:

```text
call_89342__thought__<base64 signature>
```

On the way back out, the id is split at the first `__thought__`, the raw id goes
into `functionCall.id`, and the signature goes into `part.thoughtSignature`.
Signatures may themselves contain the separator, so only the first occurrence
splits.

## Tool declarations

Use `parametersJsonSchema`, not `parameters`. The upstream validates a JSON
Schema subset and rejects unknown keywords, so the gateway sanitizes:

- `$ref` / `$defs` resolved in place, sibling `description` preserved
- `allOf` flattened into one object schema
- `anyOf` / `oneOf` of `{"const": …}` collapsed to `{"type": "string", "enum": […]}`
- `"type": ["string", "null"]` → `"string"` plus a description hint
- `format` / `pattern` / `const` / `examples` demoted into `description`
- cycles terminated with an object placeholder

`toolConfig.functionCallingConfig.mode`:

| mode | behavior |
| :--- | :--- |
| `AUTO` | model decides |
| `VALIDATED` | default used here; normal agency, schema-checked |
| `ANY` | must call one of `allowedFunctionNames` (structural forcing) |
| `NONE` | no tool calls |

## Model mapping

The client-facing `-low/-medium/-high` suffixes are UI sugar; the backend accepts
one tiered id plus a reasoning level.

| Client selector | Upstream model | `thinkingLevel` |
| :--- | :--- | :--- |
| `gemini-3.8-flash-low` | `gemini-3.8-flash-tiered` | `LOW` |
| `gemini-3.8-flash-medium` / `gemini-3.8-flash` | `gemini-3.8-flash-tiered` | `MEDIUM` |
| `gemini-3.8-flash-high` | `gemini-3.8-flash-tiered` | `HIGH` |
| `gemini-4-argon-*` | `gemini-3.8-flash-tiered` | suffix |
| `claude-sonnet-4-6` / `claude-sonnet-5-5*` | `claude-sonnet-4-6` | — |
| `claude-opus-4-6*` / `claude-opus-5-5*` | `claude-opus-4-6-thinking` | — |
| `gpt-oss-120b*` | `gpt-oss-120b-medium` | — |
| `gemini-3-flash` | `gemini-3-flash` | — |

`fetchAvailableModels` on a `standard-tier` consumer account returned:
`gemini-3.8-flash-tiered`, `gemini-3-flash`, `gemini-3.1-flash-lite`,
`gemini-3.1-flash-image`, `gemini-2.5-pro`, `gemini-2.5-flash`,
`claude-sonnet-4-6`, `claude-opus-4-6-thinking`, `gpt-oss-120b-medium`.

Sending a client-side alias as the upstream `model` yields
`404 Requested entity was not found`; resolve it first.

## `loadCodeAssist`

```json
POST /v1internal:loadCodeAssist
{"metadata": {"ideType": "ANTIGRAVITY", "platform": "DARWIN_ARM64", "pluginType": "GEMINI"}}
```

Response carries `cloudaicompanionProject` (used as `project` in the envelope),
`currentTier`, `allowedTiers`, and `ineligibleTiers`. A non-empty
`ineligibleTiers` is a **client-side** verdict — see
[architecture.md](architecture.md).

## Failure codes

| Status | Meaning | Typical cause |
| :--- | :--- | :--- |
| `401 UNAUTHENTICATED` | bad/expired bearer | token not refreshed, or refresh rotated elsewhere |
| `400 INVALID_ARGUMENT` | malformed request | `functionResponse.name` mismatch, bad schema keyword, unpaired tool call |
| `404 NOT_FOUND` | unknown model id | client alias sent upstream |
| `429 RESOURCE_EXHAUSTED` | quota | per-account limits |
| `503 UNAVAILABLE` | `No capacity available for model X` | transient upstream capacity |