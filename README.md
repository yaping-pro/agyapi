# agyapi

**Turn a region-locked Antigravity account into a local OpenAI-compatible API —
without a patched vendor binary, without a subprocess, without a VPN.**

```bash
curl -s http://127.0.0.1:7862/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gemini-3.8-flash-high","messages":[{"role":"user","content":"hi"}]}'
```

---

## The problem

An Antigravity account can be valid and still be unusable, because the vendor
CLI checks your region **on the client**:

```text
error: Eligibility check failed: Your current account is not eligible for
Antigravity, because it is not currently available in your location.
```

The backend, meanwhile, keeps serving that same account:

```json
POST /v1internal:loadCodeAssist
{
  "currentTier": { "id": "standard-tier", "name": "Antigravity" },
  "cloudaicompanionProject": "aicode-consumers",
  "ineligibleTiers": [ { "tierId": "free-tier", "reasonCode": "UNSUPPORTED_LOCATION" } ]
}
```

`currentTier` is granted. The location reason is attached only to the unused
`free-tier` entry. A request to `v1internal:streamGenerateContent` carrying that
account's OAuth token returns `200` and real completions.

**The region gate is cosmetic at the transport layer.** `agyapi` therefore
ignores the CLI entirely: it speaks the upstream protocol directly over HTTPS and
exposes it as `/v1/chat/completions`.

Consequence worth stating plainly: **you do not need a patched CLI, a wrapper
script, or a TUN-mode proxy.** If you already maintain one, this replaces it.

---

## How it works

```text
OpenAI client ──POST /v1/chat/completions──► agyapi ──HTTPS──► daily-cloudcode-pa
(omp, Cursor, SDK)          :7862             │               .googleapis.com
                                             │               /v1internal:…
        ◄───── SSE chunks ◄──────────────────┘
```

- `tools[]` → Gemini `functionDeclarations`; `functionCall` → OpenAI `tool_calls`.
  No XML, no text protocol.
- The upstream `thoughtSignature` is carried inside the `tool_call_id`
  (`call_x__thought__<sig>`) so multi-turn tool loops validate.
- `usageMetadata` is relayed as OpenAI `usage`, so client-side context accounting
  is real.
- The conversation is a structured `contents[]` array — there is no per-step
  prompt budget to truncate, and no vendor-side tool loop to hijack the request.

More: [docs/architecture.md](docs/architecture.md) ·
[docs/upstream-notes.md](docs/upstream-notes.md) ·
[docs/troubleshooting.md](docs/troubleshooting.md)

---

## Quick start

### Requirements

- Go 1.24+ to build (standard library only, no `require` block)
- An Antigravity account, already logged in once on this machine

### 1. Build

```bash
git clone https://github.com/yaping-pro/agyapi.git && cd agyapi
make build          # or: go build -o agyapi ./cmd/agyapi
```

### 2. Point it at your credential

The token file is whatever an interactive login produced. Default lookup:

```text
~/.local/share/agyapi/home/.gemini/antigravity-cli/antigravity-oauth-token
~/.gemini/antigravity-cli/antigravity-oauth-token            # fallback
```

Override with `-token-path` or `AGY_TOKEN_PATH`. The file is JSON with a
`token.access_token` / `token.refresh_token` / `token.expiry` object; the gateway
refreshes it in place (rotating the refresh token) and writes back atomically
with mode `0600`.

If you do not have one yet, run any interactive Antigravity login. A convenient
wrapper that isolates `HOME` to the gateway data directory:

```bash
./scripts/agy-login    # writes the token the gateway reads
```

### 3. Run

```bash
./agyapi                                  # 127.0.0.1:7862
./agyapi -port 8080 -verbose              # verbose logs, never includes tokens
```

```bash
curl -s http://127.0.0.1:7862/health
# {"status":"ok","backend":"native","token_ready":true,...}
```

### 4. Point a client at it

Any OpenAI-compatible client works. Example (`models.yml` style):

```yaml
providers:
  antigravity-local:
    baseUrl: http://127.0.0.1:7862/v1
    api: openai-completions
    auth: none          # loopback only
    discovery:
      type: openai-models-list
```

```python
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:7862/v1", api_key="local")
print(client.chat.completions.create(
    model="gemini-3.8-flash-high",
    messages=[{"role": "user", "content": "hi"}],
).choices[0].message.content)
```

---

## Configuration

Flags and environment variables are equivalent; flags win.

| Flag | Env | Default | Meaning |
| :--- | :--- | :--- | :--- |
| `-host` | `HOST` | `127.0.0.1` | bind address |
| `-port` | `PORT` | `7862` | listen port |
| `-backend` | `AGY_BACKEND` | `native` | `native` (HTTP) or `print` (legacy CLI) |
| `-token-path` | `AGY_TOKEN_PATH` | see above | OAuth token file |
| `-project` | `AGY_PROJECT` | `aicode-consumers` | `cloudaicompanionProject` |
| `-upstream-url` | `AGY_UPSTREAM_URL` | `https://daily-cloudcode-pa.googleapis.com` | upstream base |
| `-agy-path` | `AGY_PATH` | gateway `bin/agy-patched` | CLI binary, `print` backend only |
| `-default-model` | `DEFAULT_MODEL` | `gemini-3.8-flash-high` | fallback model |
| `-max-concurrency` | `MAX_CONCURRENCY` | `4` | in-flight upstream requests |
| `-verbose` | `VERBOSE` | `false` | debug logging |
| — | `AGY_OAUTH_TOKEN_URL` | `https://oauth2.googleapis.com/token` | refresh endpoint |
| — | `AGY_OAUTH_CLIENT_ID` / `_SECRET` | vendor installed-app client | override if it rotates |

---

## Models

Client selectors are mapped to upstream ids and a reasoning level:

| Selector | Upstream | Thinking |
| :--- | :--- | :--- |
| `gemini-3.8-flash-low` | `gemini-3.8-flash-tiered` | `LOW` |
| `gemini-3.8-flash` / `-medium` | `gemini-3.8-flash-tiered` | `MEDIUM` |
| `gemini-3.8-flash-high` | `gemini-3.8-flash-tiered` | `HIGH` |
| `claude-sonnet-4-6` | `claude-sonnet-4-6` | — |
| `claude-opus-4-6-thinking` | `claude-opus-4-6-thinking` | — |
| `gpt-oss-120b-medium` | `gpt-oss-120b-medium` | — |
| `gemini-3-flash` | `gemini-3-flash` | — |

Legacy aliases (`gemini-4-argon-*`, `claude-*-5-5-*`, `claude-*-4-6`) still
resolve. Unknown names are forwarded unchanged, so a newly granted model works
without a code change. `GET /v1/models` lists what this build advertises.

---

## Backends

| Backend | Transport | Use when |
| :--- | :--- | :--- |
| `native` *(default)* | direct HTTPS | always, unless the upstream contract changes |
| `print` | `agy` subprocess, text prompt, XML tool calls | last-resort fallback |

`print` is kept for resilience, not recommended: it is subject to the CLI's
16,384-token per-step input cap (which silently drops the tail of long
conversations), its built-in tool loop (which can execute your task behind the
caller's back), and its Keychain attempt (which raises a macOS dialog). The code
lives under `internal/print/` and is labelled accordingly.

---

## Security

- Binds to `127.0.0.1`. **There is no auth layer** — anything that can reach the
  port can spend your quota. Do not expose it directly.
- Token file is `0600`, written via atomic rename; the gateway never touches the
  macOS Keychain and never logs token values.
- Single-user by design: rotation, concurrency and quota accounting are not
  multi-tenant safe.

Details and disclosure process: [SECURITY.md](SECURITY.md).

---

## Limitations

- Single account. Multi-account pooling is out of scope.
- Stateless: no conversation history, no prompt cache beyond `sessionId` affinity.
- Unofficial protocol. `docs/upstream-notes.md` records what was observed; it can
  change without notice.
- No image generation yet (`inlineData` input is supported; image output is not
  wired up).
- The `print` backend inherits every CLI quirk.

---

## Development

```bash
make check        # gofmt + vet + tests (what CI runs)
make test-race
make build
```

Layout and conventions: [CONTRIBUTING.md](CONTRIBUTING.md).
Release history: [CHANGELOG.md](CHANGELOG.md).
The original subprocess-era design notes are preserved under `docs/design/` for
provenance.

If you publish this under your own module path, update the `module` line in
`go.mod` so `go install <path>/cmd/agyapi@latest` resolves.

## License

[MIT](LICENSE).

## Disclaimer

Independent interoperability tool. Not affiliated with, endorsed by, or supported
by Google. Reverse-proxying a subscription may breach the provider's terms of
service; that risk belongs to the operator. Use it only with an account you own
and only where you are permitted to.