# Contributing

Thanks for taking a look. This project is deliberately small: standard-library
Go, no external dependencies, no code generation.

## Ground rules

- **No new dependencies.** If a change seems to need one, open an issue first.
- **No credentials in the tree.** Never commit a token file, an `.env`, or a
  captured HTTP body containing `Authorization`. See [SECURITY.md](SECURITY.md).
- **Tests accompany behavior changes.** Translation logic (`internal/convert`)
  and the token store (`internal/auth`) are the parts where silent breakage is
  invisible at runtime, so they carry the heaviest coverage.

## Development

```bash
make check      # gofmt + vet + tests, same as CI
make test-race
make build
make run        # 127.0.0.1:7862
```

Go 1.24 or newer. The module is standard-library only, so `go mod tidy` should
never introduce a `require` block.

## Where things live

| Path | Responsibility |
| :--- | :--- |
| `cmd/agyapi` | process wiring: flags, logging, signal handling |
| `internal/config` | defaults, env/flag precedence, path resolution |
| `internal/server` | HTTP surface, middleware, SSE framing |
| `internal/convert` | OpenAI ⇄ Gemini translation (schema, tools, signatures) |
| `internal/antigravity` | upstream HTTP transport + SSE reader |
| `internal/auth` | OAuth token file + refresh |
| `internal/openai` | wire types shared by the HTTP layer |
| `internal/print` | legacy CLI-subprocess backend (see `docs/architecture.md`) |

## Commit style

Conventional-ish, imperative, scoped by package:

```
feat(convert): recover tool names from history
fix(auth): adopt peer-rotated refresh tokens
docs(readme): document credential recovery
```

## Pull requests

- One logical change per PR.
- Describe the failure mode you observed, not just the fix. A short
  before/after observation (status code, log line, test name) is worth more than
  prose.
- If you cannot verify against a live upstream, say so explicitly. Unit tests
  with `httptest` fixtures are acceptable, but do not claim live verification.

## Reporting bugs

Include:

- `agyapi` version / commit
- backend (`native` or `print`), model selector
- the exact upstream status + body if visible in the logs
- whether the failure is per-request or persistent

Do **not** paste tokens, refresh tokens, or full `Authorization` headers.