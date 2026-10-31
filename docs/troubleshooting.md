# Troubleshooting

Start here:

```bash
curl -s http://127.0.0.1:7862/health
```

```json
{"status":"ok","backend":"native","token_ready":true,"agy_available":false, ...}
```

- `token_ready: false` → the gateway could not read a usable token. See
  [Token problems](#token-problems).
- `backend: print` → you are on the legacy path; `agy_available` must be `true`.

---

## Token problems

### `401 UNAUTHENTICATED` on every request

The access token is missing or expired and the refresh did not happen.

1. Confirm the file the gateway is actually reading:
   `agyapi --help` shows the resolved `-token-path`, or run
   `curl -s http://127.0.0.1:7862/health` and compare with
   `AGY_TOKEN_PATH`.
2. Confirm the refresh credential exists:
   ```bash
   python3 -c "import json;d=json.load(open('<token path>'));print(bool(d['token'].get('refresh_token')))"
   ```
3. If `refresh_token` is empty, or refresh returns `invalid_grant`, the
   credential is revoked. Re-authenticate:
   ```bash
   agy-login
   ```
   (Any equivalent interactive login that writes the same file works too.)

### `refresh rejected ... invalid_grant`

Google rotates the refresh token on every successful refresh. This error means
the stored refresh token was already spent — typically because two processes
raced. The gateway re-reads the file and adopts a peer's state, so a single
collision self-heals; a persistent error means the credential is truly gone.

Do not run a second refresher in a loop against the same account. Either let the
gateway own refreshes, or point it at a separate credential.

### macOS offers `A keychain cannot be found to store "antigravity"`

Something invoked the vendor CLI with an isolated `HOME`. Click **Cancel** — the
CLI falls back to file storage and writes the token where the gateway reads it.

The gateway itself never touches the Keychain, so this dialog cannot come from it.

---

## Request problems

### `400 INVALID_ARGUMENT: Request contains an invalid argument`

The upstream rejected the body. Usual causes:

| Cause | Check |
| :--- | :--- |
| `functionResponse.name` does not match the issued `functionCall.name` | clients send only `tool_call_id`; the gateway recovers the name from history — a mismatch means the assistant turn was trimmed by the caller |
| a tool schema keyword the backend rejects | decls must use `parametersJsonSchema` and pass through the sanitizer |
| an assistant `tool_calls` entry with no corresponding `tool` message | every call needs exactly one response before the next turn |

Reproduce with the smallest payload that still fails, then bisect the messages.

### `404 NOT_FOUND: Requested entity was not found`

A client-side model alias was sent as the upstream id. Use the selectors listed
in [upstream-notes.md](upstream-notes.md#model-mapping), or extend
`convert.ResolveModel`.

### `503 UNAVAILABLE: No capacity available for model X`

Upstream capacity, not your request. Retry, or pick another family
(`gemini-3-flash`, a Claude model) for that turn.

### Streaming stops with `finish_reason: length`

The model hit its output ceiling. Raise `max_tokens`, or let the caller
continue the turn. On the `print` backend this symptom also appears when the CLI
truncates the *input*; switch to `-backend native`.

### Long conversations lose the latest instruction

You are on `-backend print`: the CLI caps a single input step at 16,384 tokens
and amputates the tail. Switch to `-backend native`.

---

## Account problems

### The vendor CLI says the account is not available in this location

Expected. The check is client-side; the backend still serves the account (see
[architecture.md](architecture.md#the-problem-this-solves)). `agyapi` bypasses
the check by construction — it does not ask the CLI for permission.

If the *gateway* returns upstream errors about the location, then the tier really
is not granted for that account, and no client-side work will fix it.

### Quota exhausted / usage not counted

`usageMetadata` is reported upstream. If a client shows zero usage, it is
ignoring the `usage` field on the terminal stream chunk.

---

## Operational

### Port already in use

```bash
lsof -nP -iTCP:7862 -sTCP:LISTEN
```

### Verbose logs

```bash
./agyapi -verbose
```

Token values are never logged at any level.

### Resetting everything

Only two things are persisted: the binary and the token file. Delete the token
file to force a re-login; delete nothing else.