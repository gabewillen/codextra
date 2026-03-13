# Repository Integrations Map

## Scope

This document maps real integration boundaries in the repository: upstream APIs, auth providers, local services, transport protocols, persistence backends, OS integrations, and packaging/runtime handoffs.

## Upstream Model And Account APIs

### OpenAI Responses API

- The default API wire contract is OpenAI Responses, not Chat Completions. `codex-rs/core/src/model_provider_info.rs` only accepts `wire_api = "responses"`.
- Default API-key mode points at `https://api.openai.com/v1`; ChatGPT-auth mode points at `https://chatgpt.com/backend-api/codex`; both are assembled in `codex-rs/core/src/model_provider_info.rs`.
- HTTP request construction, retries, SSE streaming, and websocket variants live in:
  - `codex-rs/codex-api/src/provider.rs`
  - `codex-rs/codex-api/src/endpoint/responses.rs`
  - `codex-rs/codex-api/src/endpoint/responses_websocket.rs`
  - `codex-rs/codex-api/src/endpoint/realtime_websocket/`
- Provider-level overrides support custom `base_url`, `query_params`, static headers, env-backed headers, retry counts, and idle timeouts in `codex-rs/core/src/model_provider_info.rs`.

### ChatGPT backend / Codex backend

- A separate backend client talks to task, usage, and config endpoints exposed as either:
  - `/api/codex/...`
  - `/wham/...`
- Path normalization and endpoint mapping live in `codex-rs/backend-client/src/client.rs`.
- Concrete backend operations include:
  - usage/rate limits
  - task list and task detail fetches
  - sibling turn lookup
  - managed requirements fetch
  - task creation
- This is a real service boundary used beyond raw model inference; it is not just a model proxy.

### Azure and OpenAI-compatible providers

- Azure-style Responses endpoints are detected in `codex-rs/codex-api/src/provider.rs`.
- The repo intentionally keeps bundled providers minimal: `openai`, `ollama`, and `lmstudio` are built in; everything else is expected through config-defined `model_providers` in `config.toml`. See `codex-rs/core/src/model_provider_info.rs`.
- Provider auth can come from:
  - OpenAI/ChatGPT managed auth
  - provider-specific env vars via `env_key`
  - experimental inline bearer tokens

## Local OSS Provider Integrations

### Ollama

- Built-in provider id: `ollama` in `codex-rs/core/src/model_provider_info.rs`.
- Default endpoint: `http://localhost:11434/v1`.
- Native Ollama readiness/model-management logic lives in:
  - `codex-rs/ollama/src/lib.rs`
  - `codex-rs/ollama/src/client.rs`
  - `codex-rs/ollama/src/url.rs`
- Codex can detect whether the configured base URL is OpenAI-compatible and can fall back to Ollama-native host roots.

### LM Studio

- Built-in provider id: `lmstudio` in `codex-rs/core/src/model_provider_info.rs`.
- Default endpoint: `http://localhost:1234/v1` via OSS provider wiring.
- Integration code is in:
  - `codex-rs/lmstudio/src/lib.rs`
  - `codex-rs/lmstudio/src/client.rs`
- The code can locate the `lms` binary and call LM Studio model/download endpoints.

## Authentication And Credential Storage

### ChatGPT OAuth login

- Interactive browser-based auth runs a short-lived localhost callback server in `codex-rs/login/src/server.rs`.
- Default issuer is `https://auth.openai.com`; default callback port is `1455`.
- The login flow uses PKCE helpers from `codex-rs/login/src/pkce.rs`.
- Device-code login is implemented separately in `codex-rs/login/src/device_code_auth.rs`.

### Auth persistence

- Auth state is stored in `$CODEX_HOME/auth.json` and/or the OS keyring depending on credential store mode; see `codex-rs/core/src/auth/storage.rs`.
- `AuthDotJson` is the expected on-disk structure in `codex-rs/core/src/auth/storage.rs`.
- Refresh-token handling and managed/external auth logic live in `codex-rs/core/src/auth.rs`.
- Secrets unrelated to account auth use the local secrets backend in `codex-rs/secrets/`, with key material stored through `codex-rs/keyring-store/`.

## MCP And Connector Boundaries

### Codex as MCP client

- Codex loads configured MCP servers from `config.toml` and merges plugin-provided servers in `codex-rs/core/src/mcp/mod.rs`.
- MCP server config supports streamable HTTP, headers, env-backed headers, bearer token env vars, scopes, and OAuth resources; see config handling in `codex-rs/core/src/config/` plus app-server schema in `codex-rs/app-server-protocol/src/protocol/v2.rs`.
- MCP auth status computation and OAuth storage modes are part of `codex-rs/core/src/mcp/`.
- Skill-driven MCP dependency prompts/install flow is implemented in `codex-rs/core/src/mcp/skill_dependencies.rs`.

### Codex as MCP server

- `codex mcp-server` is implemented in `codex-rs/mcp-server/`.
- The server exposes Codex tools to external MCP clients; command, patch, and approval handling are in:
  - `codex-rs/mcp-server/src/codex_tool_runner.rs`
  - `codex-rs/mcp-server/src/exec_approval.rs`
  - `codex-rs/mcp-server/src/patch_approval.rs`

### OpenAI connectors / apps gateway

- Codex can synthesize a special MCP server named `codex_apps`; wiring lives in `codex-rs/core/src/mcp/mod.rs`.
- Gateway targets are:
  - `https://api.openai.com/v1/connectors/gateways/flat/mcp`
  - ChatGPT backend `/wham/apps`
  - Codex backend `/api/codex/apps`
- Auth is forwarded through bearer tokens and `ChatGPT-Account-ID` headers.

### Experimental shell-tool MCP package

- `shell-tool-mcp/README.md` defines an external MCP server that provides a `shell` tool using patched Bash/Zsh binaries.
- It relies on the custom `codex/sandbox-state` MCP capability so Codex can push sandbox policy updates to the server.
- This package is an integration point between Codex CLI, MCP clients, and native shell wrapper binaries.

## App-Server And Client Transports

### Codex app-server

- `codex app-server` is the IDE/client integration surface; see `codex-rs/app-server/README.md`.
- Transport options:
  - stdio JSONL: `stdio://`
  - websocket text-frame JSON-RPC: `ws://IP:PORT`
- Transport parsing and channel wiring are implemented in `codex-rs/app-server/src/transport.rs`.
- The schema and RPC names are defined in `codex-rs/app-server-protocol/src/protocol/v2.rs` and exported to TS/JSON schema fixtures under `codex-rs/app-server-protocol/schema/`.

### App-server feature surface

- Stable/high-value integration areas exposed over JSON-RPC include:
  - threads and turns
  - direct `command/exec`
  - model listing
  - account/login/logout/rate-limits
  - config read/write and config requirements
  - MCP server status and OAuth login
  - feedback upload
  - skills/plugin/app listing and install flows
- A machine-readable method inventory is exported in `codex-rs/app-server-protocol/schema/typescript/ClientRequest.ts`.

### TypeScript SDK

- The SDK in `sdk/typescript/` does not talk to app-server directly.
- Instead it spawns the CLI as a subprocess and exchanges JSONL over stdin/stdout; this is explicit in `sdk/typescript/README.md` and `sdk/typescript/src/exec.ts`.
- The SDK injects environment overrides such as `OPENAI_BASE_URL`, `CODEX_API_KEY`, and `CODEX_INTERNAL_ORIGINATOR_OVERRIDE`.

## Local Persistence And Storage Integrations

### SQLite state database

- `codex-rs/state/` mirrors rollout metadata into local SQLite.
- Database home is controlled by `sqlite_home` / `CODEX_SQLITE_HOME`; see `docs/config.md` and `codex-rs/state/src/lib.rs`.
- Schema migrations live in:
  - `codex-rs/state/migrations/`
  - `codex-rs/state/logs_migrations/`
- This is local-only persistence; there is no repo-local integration with Postgres/MySQL/Redis.

### Rollout/session files

- Thread histories are persisted under `~/.codex/sessions`, documented in `sdk/typescript/README.md`.
- The state DB is deliberately a mirror/index over rollout metadata, not the source conversation store; `codex-rs/state/src/lib.rs` states rollout scanning/orchestration stays in `codex-core`.

### Secrets and keyring

- `codex-rs/secrets/` stores named secrets under scoped namespaces such as global or environment-specific scopes.
- Key material is stored in the OS keyring through `codex-rs/keyring-store/`.
- Secret names are normalized and environment IDs can be derived from repo roots or cwd hashes in `codex-rs/secrets/src/lib.rs`.

## Network, Sandbox, And OS Integrations

### Managed network proxy

- `codex-rs/network-proxy/` is a local HTTP + SOCKS5 proxy enforcing allow/deny policies.
- Default listeners are documented in `codex-rs/network-proxy/README.md`:
  - HTTP proxy: `127.0.0.1:3128`
  - SOCKS5 proxy: `127.0.0.1:8081`
- It supports:
  - allowlists / denylists
  - limited read-only mode
  - upstream proxy forwarding
  - optional MITM for HTTPS CONNECT enforcement
  - macOS unix-socket proxying
- Integration with higher-level exec approval happens through the proxy decider hook documented in `codex-rs/network-proxy/README.md`.

### Linux sandbox helper

- `codex-rs/linux-sandbox/` builds the `codex-linux-sandbox` helper used by Linux runs.
- The active pipeline combines vendored bubblewrap plus in-process seccomp/Landlock behavior; see `codex-rs/linux-sandbox/README.md`, `codex-rs/linux-sandbox/src/bwrap.rs`, and `codex-rs/linux-sandbox/src/landlock.rs`.
- Managed-proxy mode can route sandboxed traffic through the local proxy bridge only.

### macOS and Windows

- macOS sandboxing and permissions surfaces are represented through Seatbelt-related code in `codex-rs/core/src/seatbelt_permissions.rs` and related model/protocol types.
- Windows integration includes dedicated sandbox binaries in `codex-rs/windows-sandbox-rs/` and app-server setup RPCs exposed from `codex-rs/app-server-protocol/src/protocol/v2.rs`.

### Responses API proxy helper

- `codex-rs/responses-api-proxy/` is a minimal local HTTP helper.
- It only permits `POST /v1/responses` and forwards to an upstream URL, defaulting to `https://api.openai.com/v1/responses`; see `codex-rs/responses-api-proxy/src/lib.rs`.
- It reads the auth header from stdin and can expose a local shutdown endpoint.

## Hooks, Notifications, And Feedback

### Local hook execution

- Hook execution is local-process integration, not a webhook system.
- Supported events are `after_agent` and `after_tool_use`; payload shape is defined in `codex-rs/hooks/src/types.rs`.
- Hook payloads include cwd, session/thread ids, tool metadata, sandbox details, and output previews.

### Notifications and desktop surfaces

- Public docs in `codex-rs/README.md` and `docs/config.md` describe a notify hook plus desktop notification behavior.
- The notify hook is wired through `codex-rs/hooks/src/registry.rs`.

### Feedback upload

- App-server exposes `feedback/upload` as an external boundary; this is documented in `codex-rs/app-server/README.md` and typed in `codex-rs/app-server-protocol/src/protocol/v2.rs`.

## Observability And Audit Integrations

- `codex-rs/otel/` provides OTLP telemetry export, runtime metrics, trace context propagation, and session telemetry.
- The network proxy emits audit-style OTEL events documented in `codex-rs/network-proxy/README.md`.
- App-server logging can emit JSON via `LOG_FORMAT=json`; see `codex-rs/app-server/README.md`.

## Packaging And Distribution Integrations

- npm distribution wraps native binaries in `codex-cli/`.
- Homebrew and GitHub Releases are part of the release model documented in `README.md` and `codex-rs/README.md`.
- Dockerized Linux sandboxing for the legacy JS CLI is defined in `codex-cli/Dockerfile` and `codex-cli/scripts/run_in_container.sh`.
- Release automation, signing, and staging span:
  - `.github/workflows/rust-release.yml`
  - `.github/workflows/rust-release-windows.yml`
  - `.github/workflows/shell-tool-mcp.yml`

## Practical Integration Summary

- External APIs: OpenAI Responses, ChatGPT backend/Codex backend, optional Azure-compatible providers, Ollama, LM Studio.
- External protocols: JSON-RPC over stdio/websocket, MCP over stdio/streamable HTTP, SSE, websocket realtime.
- Local persistence: rollout files, SQLite, auth.json, OS keyring, encrypted secrets storage.
- Local system boundaries: shells, PTYs, proxies, sandbox helpers, browser launch, localhost OAuth callback, Docker packaging.
- Missing categories: no first-party SQL server, no direct cloud message queue, and no inbound webhook server in the product path.
