# Repository Stack Map

## Scope

This repository is a mixed Rust + TypeScript monorepo. The maintained product is the native Rust Codex CLI in `codex-rs/`; the PNPM workspace mainly packages binaries, ships a TypeScript SDK, and builds the experimental `shell-tool-mcp` package.

## Primary Languages And Runtimes

- Rust 2024 edition is the main implementation language. The Cargo workspace and shared dependency graph live in `codex-rs/Cargo.toml`.
- TypeScript runs in the PNPM workspace declared in `pnpm-workspace.yaml`. Package roots are `codex-cli/`, `sdk/typescript/`, `shell-tool-mcp/`, and `codex-rs/responses-api-proxy/npm/`.
- Node.js is required for the JS/TS packages. The repo root pins `node >=22` and `pnpm >=10.29.3` in `package.json`; package-level minimums vary:
  - `codex-cli/package.json` declares `node >=16`.
  - `sdk/typescript/package.json` declares `node >=18`.
  - `shell-tool-mcp/package.json` declares `node >=18`.
- Shell and system integration matter operationally: the code targets `zsh`, `bash`, PTYs, OS keyrings, macOS Seatbelt, Linux bubblewrap/Landlock/seccomp, and Windows sandbox helpers. See `codex-rs/core/src/tools/`, `codex-rs/linux-sandbox/`, `codex-rs/shell-escalation/`, and `codex-rs/windows-sandbox-rs/`.

## Top-Level Runtime Components

### Rust product surface

- `codex-rs/cli/`: multi-tool CLI entrypoint, exposes `codex`.
- `codex-rs/tui/`: fullscreen terminal UI built on Ratatui/Crossterm.
- `codex-rs/exec/`: non-interactive/headless execution mode.
- `codex-rs/core/`: orchestration, tools, config loading, auth, providers, memory, sandbox logic.
- `codex-rs/protocol/`: shared internal/external protocol types.
- `codex-rs/app-server/`: JSON-RPC server used by IDEs and rich clients.
- `codex-rs/app-server-protocol/`: versioned app-server schema/types plus TS/JSON schema export.
- `codex-rs/mcp-server/`: Codex acting as an MCP server for other agents.

### TypeScript / packaging surface

- `codex-cli/`: npm package that ships platform-specific native binaries and the launcher in `codex-cli/bin/codex.js`.
- `sdk/typescript/`: SDK that spawns `codex exec --experimental-json` and streams JSONL events; see `sdk/typescript/src/exec.ts` and `sdk/typescript/src/thread.ts`.
- `shell-tool-mcp/`: experimental MCP package that bundles patched Bash/Zsh selectors and TS wrapper code; see `shell-tool-mcp/README.md`.

## Major Rust Frameworks And Libraries

### Async, process, terminal

- `tokio` is the async runtime across the workspace (`codex-rs/Cargo.toml`).
- `portable-pty` and internal PTY helpers back streaming command execution in `codex-rs/utils/pty/` and `codex-rs/core/src/unified_exec/`.
- `crossterm`, `ratatui`, `ratatui-macros`, `vt100`, `syntect`, `pulldown-cmark`, and `textwrap` power the TUI in `codex-rs/tui/Cargo.toml`.

### HTTP, streaming, and realtime

- `reqwest` is the main HTTP client stack.
- `eventsource-stream` handles SSE-style response streams.
- `tokio-tungstenite` and `tungstenite` handle websocket transports in `codex-rs/codex-api/`, `codex-rs/app-server/`, and `codex-rs/app-server-test-client/`.
- `http` and `bytes` are used in lower-level request construction in `codex-rs/codex-api/`.

### Protocols and schemas

- `rmcp` is the MCP implementation base for both MCP client and server flows in `codex-rs/core/`, `codex-rs/mcp-server/`, and `codex-rs/app-server-protocol/`.
- `serde`, `serde_json`, `serde_yaml`, `toml`, and `toml_edit` handle config and wire formats.
- `schemars` generates JSON Schema.
- `ts-rs` exports TypeScript types from Rust types in `codex-rs/app-server-protocol/`.

### Persistence, secrets, and auth

- `sqlx` with SQLite features backs local state in `codex-rs/state/`.
- `keyring` and `codex-rs/keyring-store/` integrate with OS credential stores.
- `age`, `sha2`, and local secret storage power `codex-rs/secrets/`.

### Observability and diagnostics

- `tracing`, `tracing-subscriber`, and `tracing-appender` are the default logging stack.
- `opentelemetry`, `opentelemetry-otlp`, `tracing-opentelemetry`, and `codex-rs/otel/` provide OTLP telemetry and audit events.
- `sentry` is present in the workspace dependency set in `codex-rs/Cargo.toml`, but the active observability surface is clearly OTEL-first.

### Sandboxing and network control

- Linux sandboxing uses `landlock`, `seccompiler`, and vendored `bubblewrap`; see `codex-rs/linux-sandbox/`.
- The managed proxy uses Rama crates (`rama-http`, `rama-socks5`, `rama-tls-rustls`, etc.) in `codex-rs/network-proxy/Cargo.toml`.

## Major TypeScript Tooling

- Bundling uses `tsup` in `sdk/typescript/package.json` and `shell-tool-mcp/package.json`.
- Testing uses `jest` plus `ts-jest`; configs live in `sdk/typescript/jest.config.cjs` and `shell-tool-mcp/jest.config.cjs`.
- Linting uses `eslint` and `typescript-eslint` in `sdk/typescript/eslint.config.js`.
- Formatting uses `prettier` at the repo root and in package-local scripts.
- JSON schema support for SDK users is built around `zod` and `zod-to-json-schema` in `sdk/typescript/package.json`.

## Build, Packaging, And Release Tooling

### Local build orchestration

- The root `justfile` is the main developer command surface for Rust work:
  - `just fmt`
  - `just fix`
  - `just test`
  - `just bazel-test`
  - `just write-config-schema`
  - `just write-app-server-schema`
- Rust builds use Cargo workspace commands rooted at `codex-rs/`.
- The repository also maintains Bazel targets and lockfiles; see `MODULE.bazel.lock`, `codex-rs/*/BUILD.bazel`, and `.github/workflows/bazel.yml`.

### Packaging

- The npm CLI package wraps prebuilt native binaries via `codex-cli/bin/codex.js`.
- Container packaging lives in `codex-cli/Dockerfile`, `codex-cli/scripts/build_container.sh`, and `codex-cli/scripts/run_in_container.sh`.
- The shell-tool MCP package is assembled from TS plus prebuilt patched shells; CI builds those assets in `.github/workflows/shell-tool-mcp.yml`.
- The app-server protocol exports generated schema fixtures with `codex-rs/app-server-protocol/src/bin/write_schema_fixtures.rs`.

### Notable dependency patching

- The Rust workspace patches upstream crates in `codex-rs/Cargo.toml`:
  - `crossterm`
  - `ratatui`
  - `tokio-tungstenite`
  - `tungstenite`
- Some dependencies are pinned to git revisions rather than crates.io, notably `nucleo` and `runfiles`.

## Test And Quality Tooling

### Rust

- Standard testing uses `cargo test`; `just test` prefers `cargo nextest`.
- Snapshot testing uses `insta`, heavily in `codex-rs/tui/src/**/snapshots/`.
- HTTP/integration mocking uses `wiremock`.
- Assertion style prefers `pretty_assertions`.
- Clippy and formatting are enforced from the workspace and CI:
  - `.github/workflows/rust-ci.yml`
  - `codex-rs/Cargo.toml` under `[workspace.lints.clippy]`

### TypeScript

- `sdk/typescript` and `shell-tool-mcp` run Jest tests in CI via `.github/workflows/sdk.yml` and `.github/workflows/shell-tool-mcp-ci.yml`.
- Root repo formatting checks are run with Prettier in `.github/workflows/ci.yml`.

### Supply-chain / repo hygiene

- `cargo-deny` runs in `.github/workflows/cargo-deny.yml`.
- `codespell` runs in `.github/workflows/codespell.yml`.
- Release signing and artifact staging are handled in `.github/workflows/rust-release.yml` and `.github/workflows/rust-release-windows.yml`.

## Configuration Surfaces

### User and managed config

- Main user config is `config.toml` under `CODEX_HOME`; public docs point to `docs/config.md`.
- Generated config schema is `codex-rs/core/config.schema.json`.
- Config layering and managed overrides live in `codex-rs/core/src/config_loader/`.
- Managed requirements and legacy managed config handling are implemented in `codex-rs/core/src/config_loader/mod.rs`.

### Important config domains

- Model/provider definitions: `codex-rs/core/src/model_provider_info.rs`
- Sandbox and approval settings: `codex-rs/protocol/src/config_types.rs`
- Permission profiles / network policy: `codex-rs/core/src/network_proxy_loader.rs`
- MCP servers and connector wiring: `codex-rs/core/src/mcp/mod.rs`
- App-server API schema: `codex-rs/app-server-protocol/src/protocol/v2.rs`
- CLI/SDK thread options: `sdk/typescript/src/threadOptions.ts`

### State and filesystem layout

- Conversation rollouts are stored under `~/.codex/sessions`; this is documented in `sdk/typescript/README.md`.
- SQLite metadata mirrors live under `sqlite_home` / `CODEX_SQLITE_HOME`; see `codex-rs/state/src/lib.rs` and `docs/config.md`.
- Auth persistence lives in `$CODEX_HOME/auth.json` and/or keyring-backed storage; see `codex-rs/core/src/auth/storage.rs`.
- Secrets live under `codex-rs/secrets/` with OS-keyring-backed key material.

## Practical Takeaways

- Treat `codex-rs/` as the product source of truth.
- Treat the PNPM workspace as packaging, SDK, and MCP-adjacent support code.
- For config-aware work, start with `docs/config.md`, `codex-rs/core/config.schema.json`, and `codex-rs/core/src/config_loader/`.
- For API/schema work, start with `codex-rs/app-server-protocol/` and `codex-rs/app-server/`.
- For sandbox/network behavior, start with `codex-rs/core/`, `codex-rs/linux-sandbox/`, and `codex-rs/network-proxy/`.
