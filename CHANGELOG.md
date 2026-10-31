# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.1.0] — 2026-10-31

The release that made the region-locked account usable without a patched vendor
binary.

### Added

- **Native upstream backend (default).** Talks to
  `POST /v1internal:streamGenerateContent?alt=sse` directly over HTTPS. No
  subprocess, no prompt compilation, no Keychain access.
- `internal/convert`: structural OpenAI ⇄ Gemini translation — `tools[]` →
  `functionDeclarations`, `functionCall` → `tool_calls`, cleaned
  `parametersJsonSchema`, `thoughtSignature` carried inside the `tool_call_id`.
- `internal/auth`: token-file store that refreshes in-process, rotates the
  refresh token atomically (`0600`), re-reads on every request, and adopts a
  peer process's rotated state instead of clobbering it.
- `-backend` flag (`native` | `print`) with `print` retained as a fallback.
- Health endpoint reports `backend` and `token_ready`.
- Default listen port is `7862` (was `8000`, which collides with common dev servers).
- `scripts/agy-login` ships with the repo so a fresh clone can obtain a credential.
- Real `usage` relayed from upstream `usageMetadata`, so client-side context and
  compaction accounting is accurate.

### Changed

- `-agy-path` now resolves to the patched CLI under the gateway data directory,
  so the fallback works even when `agy` is absent from `PATH`.
- `internal/bridge` and `internal/prompt` moved under `internal/print/` to make
  the legacy boundary explicit.
- Tool results reuse the function name recorded on the issuing assistant turn;
  OpenAI clients only send `tool_call_id`, and a mismatched
  `functionResponse.name` is rejected upstream with HTTP 400.

### Fixed

- Conversations longer than the CLI's per-step prompt budget no longer lose
  trailing instructions (observed failure: a 335 KB / ~58 K token context was
  truncated at 16,384 tokens, silently dropping the user's latest message).
- The vendor CLI's built-in `run_command` can no longer take over a request and
  execute tools outside the caller; tools are structured declarations now.
- A harness-injected `role: "developer"` reminder is kept in conversation order
  instead of being promoted to the top-level system instruction.
- macOS `A keychain cannot be found to store "antigravity"` dialog no longer
  appears, because the gateway never touches the Keychain.

## [1.0.0] — 2026-09-10

Initial release: OpenAI-compatible endpoints backed by the local `agy` CLI
subprocess. Architecture documented in `docs/design/`.

[Unreleased]: https://github.com/yaping-pro/agyapi/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/yaping-pro/agyapi/releases/tag/v1.1.0
[1.0.0]: https://github.com/yaping-pro/agyapi/releases/tag/v1.0.0