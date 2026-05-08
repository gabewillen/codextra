# AGENTS.md

## Project

`codextra` is a small Go wrapper/proxy for Codex. Keep it boring, auditable,
and easy to rebuild.

## Core Rules

- Use Go 1.26.
- Use only the Go standard library unless the user explicitly approves a new
  dependency.
- Do not add generated framework code, vendored code, or package-manager lock
  files for non-Go ecosystems.
- Keep commands simple and transparent: `codextra` should pass through to
  `codex` unless it intentionally intercepts a documented `codextra` command.
- Prefer small packages with clear ownership:
  - `cmd/codextra` for CLI dispatch and process launching.
  - `internal/accounts` for account registry/storage.
  - `internal/proxy` for HTTP/WebSocket proxy behavior.
- Keep secrets out of logs, errors, tests, and snapshots. Never print access
  tokens, refresh tokens, id tokens, or authorization headers.
- Account data written to disk must use restrictive permissions (`0600` for
  files, `0700` for directories).

## Proxy Rules

- Do not implement TLS MITM or system-wide proxying without explicit user
  approval.
- Prefer Codex's `chatgpt_base_url` config override over global proxy settings.
- The proxy must preserve streaming behavior. Do not buffer successful response
  streams beyond what is needed to inspect pre-stream error responses.
- Rotation must only happen before a response is sent to Codex. Once bytes or a
  WebSocket `101 Switching Protocols` response are sent downstream, do not
  pretend rotation can be transparent.
- If WebSocket support is incomplete, fail clearly rather than silently
  downgrading or corrupting the session.

## CLI Rules

- `codextra login <alias>` imports the active Codex auth into the codextra
  registry. Avoid owning OAuth directly unless the user asks for that.
- Validate aliases before writing account records.
- All commands that mutate the account registry must be safe to rerun and must
  preserve unrelated aliases.
- Keep output concise and script-friendly. Add JSON output for status/listing
  commands when useful.

## Testing And Verification

Before finalizing any code change, run:

```sh
gofmt -w ./cmd ./internal
go test ./...
go test -race ./...
go vet ./...
```

Coverage requirements:

- Maintain at least 90% statement coverage for non-`cmd` packages.
- For changes in `internal/accounts` or `internal/proxy`, add or update unit
  tests in the same package.
- Check coverage with:

```sh
go test ./internal/... -cover
```

Static analysis:

- Run `staticcheck ./...` before finalizing if `staticcheck` is already
  installed.
- If `staticcheck` is missing, do not install it without asking the user first.
  Report that it was not run.

Race testing:

- Run `go test -race ./...` for every code change.
- If race testing fails because of environment limitations, report the failure
  and include the relevant error.

## Git Workflow

- Keep commits focused and small.
- Do not force-push unless the user explicitly asks.
- Do not rewrite history unless the user explicitly asks.
- Before finishing, report:
  - files changed
  - tests/checks run
  - checks not run and why

