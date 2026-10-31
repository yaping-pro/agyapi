# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.1.0] — 2026-11-01

First public release. Native upstream protocol only: no vendor CLI, no
subprocess, no binary patching.

### Added

- OpenAI-compatible surface: `POST /v1/chat/completions` (streaming and
  non-streaming), `GET /v1/models`, `GET /v1/models/{model}`, `GET /health`.
- `internal/convert`: structural OpenAI ⇄ Gemini translation — `tools[]` →
  `functionDeclarations`, `functionCall` → `tool_calls`, cleaned
  `parametersJsonSchema`, `thoughtSignature` carried inside the `tool_call_id`.
- `internal/auth`: token-file store that refreshes in-process, rotates the
  refresh token atomically (`0600`), re-reads on every request, and adopts a
  peer process's rotated state instead of clobbering it.
- `scripts/agy-login` to obtain a credential with an isolated `HOME`.
- CI (gofmt, vet, race tests, build), Makefile, MIT license, bilingual README.

### Changed

- Default listen port is `7862`. Port, host, token path, project, upstream URL,
  default model and concurrency are all flag/env configurable.
- Build version is stamped by the linker from `internal/server/version.go`.

### Notes on the design

The obvious implementation — drive the vendor CLI, pipe a compiled prompt to
stdin, parse `<tool_call>` XML back — was tried first and removed from this
repository. Each of the following is a direct consequence of that shape and is
unfixable from outside it:

- long conversations silently lose the latest instruction (16,384-token
  per-step input cap in the CLI);
- the caller's tool loop is bypassed (the CLI has its own `run_command`);
- usage is always reported as 0 (text output carries no token counts);
- macOS raises `A keychain cannot be found to store "antigravity"` (the CLI
  tries Keychain before file storage).

Structured HTTP has none of these properties.

[Unreleased]: https://github.com/yaping-pro/agyapi/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/yaping-pro/agyapi/releases/tag/v1.1.0