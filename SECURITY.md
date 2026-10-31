# Security

## Scope

`agyapi` runs on your machine and holds a live OAuth credential for your
Antigravity account. Treat its data directory as sensitive as a password store.

- Bind address defaults to `127.0.0.1`. There is **no authentication layer** on
  the OpenAI-compatible endpoints — anyone who can reach the port can spend your
  quota. Do not expose it to a LAN or the internet without a fronting proxy and
  an access token.
- The gateway is single-user by design. Credential rotation, concurrency limits
  and quota accounting are not multi-tenant safe.

## Credential handling

| Item | Location | Notes |
| :--- | :--- | :--- |
| OAuth token (access + refresh) | `AGY_TOKEN_PATH` (default under `~/.local/share/agyapi/home/.gemini/antigravity-cli/`) | read/written with mode `0600`, written atomically via temp-file rename |
| OAuth client id/secret | compiled defaults, override via `AGY_OAUTH_CLIENT_ID` / `AGY_OAUTH_CLIENT_SECRET` | these identify the **installed application**, not your account (RFC 8252 §8.5); they ship inside the vendor CLI and are not confidential |

What the gateway deliberately does **not** do:

- It never talks to the macOS Keychain. Token storage is a plain file, so no
  biometric prompt and no `A keychain cannot be found to store …` dialog.
- It never logs token values. `Authorization` and refresh bodies are not
  emitted at any log level.

Google rotates the refresh token on every successful refresh. The gateway
persists the rotated value immediately; if another process rotated first, it
adopts that state instead of overwriting it.

## Disclosure

Open a private security advisory on the repository, or email the maintainers
listed in the repository profile. Do not file a public issue for credential
leaks, request-smuggling bugs, or anything that allows reaching the upstream
API with someone else's credential.

## Legal

This is an independent interoperability tool. It is not affiliated with Google.
Reverse-proxying a subscription may violate the provider's terms of service;
that risk belongs to the operator, not to the project. See the disclaimer at the
end of [README.md](README.md).