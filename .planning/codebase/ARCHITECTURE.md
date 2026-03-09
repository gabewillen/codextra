# Repository Architecture

This repository is a mixed Rust + TypeScript monorepo. The architectural center is the Rust workspace in `codex-rs/`; the root-level Node packages mostly package, wrap, or integrate that native runtime rather than replacing it.

## Architectural spine

| Layer | Responsibility | Key paths |
| --- | --- | --- |
| Packaging and distribution | Publish installable CLI packages, SDK wrappers, release artifacts, and CI entrypoints. | `README.md`, `codex-cli/`, `sdk/typescript/`, `shell-tool-mcp/`, `.github/workflows/` |
| Runtime entrypoints | Accept user/API input, resolve config/auth, and start sessions. | `codex-rs/cli/src/main.rs`, `codex-rs/tui/src/main.rs`, `codex-rs/exec/src/main.rs`, `codex-rs/app-server/src/main.rs`, `codex-rs/mcp-server/src/main.rs` |
| Core orchestration | Own session lifecycle, thread state, tools, approvals, skills, MCP, memories, and provider interaction. | `codex-rs/core/src/lib.rs`, `codex-rs/core/src/codex.rs`, `codex-rs/core/src/thread_manager.rs`, `codex-rs/core/src/codex_thread.rs` |
| Shared protocol models | Define the typed contracts between frontends, core, and external clients. | `codex-rs/protocol/src/`, `codex-rs/app-server-protocol/src/` |
| Provider and transport adapters | Translate core requests into HTTP/WebSocket/SSE calls against model providers. | `codex-rs/core/src/client.rs`, `codex-rs/codex-api/`, `codex-rs/codex-client/` |
| Persistence and background state | Persist rollouts, index metadata, maintain SQLite state, and derive memory artifacts. | `codex-rs/core/src/rollout/`, `codex-rs/state/src/`, `codex-rs/core/src/memories/` |
| Security and execution boundary | Enforce sandboxing, command policy, network policy, and tool runtimes. | `codex-rs/core/src/sandboxing/`, `codex-rs/execpolicy/`, `codex-rs/network-proxy/`, `codex-rs/shell-escalation/`, `codex-rs/process-hardening/` |

## Major runtime paths

### 1. Interactive CLI / TUI

`codex-rs/cli/src/main.rs` is the top-level multitool binary. With no subcommand it forwards to `codex-rs/tui/src/lib.rs::run_main`, which:

1. Resolves CLI overrides and config layers from `codex-rs/core/src/config/` and `codex-rs/core/src/config_loader/`.
2. Creates auth and session infrastructure through `codex-rs/core`.
3. Starts the Ratatui UI from `codex-rs/tui/src/app.rs`.
4. Talks to core using `Op` / `Event` types from `codex-rs/protocol/src/protocol.rs`.

The TUI is intentionally a presentation layer. It renders state and emits user intents; business logic stays in `codex-rs/core/`.

### 2. Non-interactive exec

`codex-rs/exec/src/lib.rs` provides the automation path used by `codex exec` and the TypeScript SDK. It:

1. Builds config and auth similarly to the TUI.
2. Creates or resumes a thread via `codex-rs/core/src/thread_manager.rs`.
3. Feeds prompts into a `CodexThread` with `Op::UserTurn` or review ops.
4. Streams events through `event_processor*.rs` into human output or JSONL.

This path shares the same core engine as the TUI; only the presentation/output layer differs.

### 3. App server

`codex-rs/app-server/src/lib.rs` hosts a long-lived JSON-RPC service for richer clients such as the VS Code extension. The path is:

`transport` -> `MessageProcessor` -> `CodexMessageProcessor` -> `ThreadManager` / `CodexThread`

Important files:

- Connection/session state and initialization: `codex-rs/app-server/src/message_processor.rs`
- Request dispatch and method implementations: `codex-rs/app-server/src/codex_message_processor.rs`
- Transport plumbing and outgoing routing: `codex-rs/app-server/src/transport.rs`, `codex-rs/app-server/src/outgoing_message.rs`
- Public wire schema: `codex-rs/app-server-protocol/src/protocol/v2.rs`

This layer is an adapter around `codex-core`, not an alternate agent implementation.

### 4. SDK wrapper

The TypeScript SDK in `sdk/typescript/src/` does not call the Rust library directly. It shells out to the native CLI:

`sdk/typescript/src/codex.ts` -> `sdk/typescript/src/thread.ts` -> `sdk/typescript/src/exec.ts` -> `codex exec --experimental-json`

That means the SDK consumes the same execution pipeline as `codex-rs/exec/`, with JSONL as the process boundary.

## Core layering inside `codex-rs/core`

### Session and thread lifecycle

`codex-rs/core/src/thread_manager.rs` is the factory and in-memory registry for active threads. It owns:

- `AuthManager`
- `ModelsManager`
- `SkillsManager`
- `PluginsManager`
- `McpManager`
- `FileWatcher`

`ThreadManager` creates `CodexThread` values from `codex-rs/core/src/codex_thread.rs`. A `CodexThread` is the public conduit that exposes:

- `submit(op)`
- `next_event()`
- `steer_input(...)`
- `config_snapshot()`

Internally it wraps `Codex` from `codex-rs/core/src/codex.rs`, which is the actual turn executor and event producer.

### Protocol boundary

There are two distinct protocol layers:

- `codex-rs/protocol/src/` defines internal session, item, approval, tool, and event types shared across Rust frontends and core.
- `codex-rs/app-server-protocol/src/` defines the external JSON-RPC schema and performs wire-shape translation for app-server clients.

This split keeps the internal `Op` / `Event` model independent from the app-server wire contract.

### Provider boundary

Model-provider interaction is layered deliberately:

- `codex-rs/codex-client/` is transport-only: HTTP primitives, retries, SSE framing.
- `codex-rs/codex-api/` is API-aware: request/response models, provider headers, streaming semantics, error mapping.
- `codex-rs/core/src/client.rs` is session-aware: conversation IDs, per-turn sessions, websocket prewarm, sticky turn state, feature-gated behavior.

Core code constructs prompts, tool lists, and session context; lower layers only move bytes and parse provider responses.

## End-to-end data flow for a turn

The normal turn path is:

1. An entrypoint builds effective config from `codex-rs/core/src/config_loader/README.md` semantics and `codex-rs/core/src/config/mod.rs`.
2. A frontend or API surface creates/resumes a thread through `codex-rs/core/src/thread_manager.rs`.
3. The caller submits an `Op` from `codex-rs/protocol/src/protocol.rs`.
4. `codex-rs/core/src/codex.rs` resolves instructions, model/provider choice, tools, approvals, MCP state, and context.
5. `ModelClientSession` in `codex-rs/core/src/client.rs` sends one or more provider requests using `codex-api`.
6. Streaming provider events are mapped back into internal events and items.
7. Tool calls are routed through `codex-rs/core/src/tools/router.rs` and executed by handlers under `codex-rs/core/src/tools/handlers/`.
8. Tool runtimes delegate to execution backends such as `codex-rs/core/src/tools/runtimes/shell.rs`, `codex-rs/core/src/tools/runtimes/apply_patch.rs`, and `codex-rs/core/src/tools/runtimes/unified_exec.rs`.
9. User-visible events are emitted back to the caller via `Event`.
10. The rollout recorder and state DB persist the session for resume, indexing, and memory extraction.

## Tooling and execution architecture

The tool system in `codex-rs/core/src/tools/` is its own subsystem:

- Tool registration/spec building: `registry.rs`, `spec.rs`
- Routing/orchestration: `router.rs`, `orchestrator.rs`, `parallel.rs`
- Execution context and event emission: `context.rs`, `events.rs`
- Tool handlers: `handlers/*.rs`
- Runtime-specific execution: `runtimes/*.rs`

Notable tool handlers include:

- File operations: `read_file.rs`, `list_dir.rs`, `grep_files.rs`
- Execution: `shell.rs`, `unified_exec.rs`, `apply_patch.rs`
- Collaboration: `multi_agents.rs`, `plan.rs`, `request_user_input.rs`
- Extensibility: `mcp.rs`, `mcp_resource.rs`, `dynamic.rs`, `artifacts.rs`, `view_image.rs`

This subsystem isolates model-facing tool descriptions from execution details and policy enforcement.

## Extensibility boundaries

### MCP, plugins, and skills

These are separate but connected layers:

- MCP connection/auth/tool discovery: `codex-rs/core/src/mcp/`, `codex-rs/rmcp-client/`, `codex-rs/mcp-server/`
- Plugin discovery/install/apps: `codex-rs/core/src/plugins/`
- Skills loading and watching: `codex-rs/core/src/skills/`, `codex-rs/core/src/file_watcher.rs`
- Built-in agent roles and delegation controls: `codex-rs/core/src/agent/`

`ThreadManager` wires these managers together so every active thread sees the same discovered skills, plugins, and MCP inventory.

### App-server as a stable integration boundary

If a consumer needs a programmatic API, the intended long-lived contract is `codex app-server`, not direct linking to `codex-core`. The public schema is generated from `codex-rs/app-server-protocol/src/protocol/v2.rs`.

## Persistence architecture

There are two persistence tracks:

### Rollouts and session files

Session transcripts and resume data live in JSONL rollout files managed by `codex-rs/core/src/rollout/`. Key pieces:

- Recorder: `recorder.rs`
- Listing/pagination: `list.rs`
- Metadata and indexing helpers: `metadata.rs`, `session_index.rs`
- Retention/truncation policy: `policy.rs`, `truncation.rs`

These files back interactive resume/fork/archive flows and support later indexing.

### SQLite state and memories

`codex-rs/state/src/lib.rs` mirrors selected rollout metadata into SQLite. `codex-rs/core/src/memories/README.md` describes a two-phase pipeline:

1. Phase 1 extracts memory candidates from eligible rollouts.
2. Phase 2 consolidates them into on-disk memory artifacts under the Codex home.

The architecture is intentionally split: rollout files remain the source transcript, while SQLite supports fast indexing, background coordination, and memory jobs.

## Security and policy layers

Security is distributed, not centralized in one crate:

- Process hardening before main: `codex-rs/process-hardening/`
- Filesystem/process sandboxing: `codex-rs/core/src/sandboxing/`, `codex-rs/linux-sandbox/`, `codex-rs/windows-sandbox-rs/`
- Command allow/prompt/deny policy: `codex-rs/execpolicy/`, `codex-rs/execpolicy-legacy/`
- Network policy enforcement: `codex-rs/network-proxy/`
- Shell escalation / exec interception: `codex-rs/shell-escalation/`

The practical boundary is: tool handlers decide what should run; sandbox, execpolicy, and network policy decide what is allowed to run and how.

## Architectural invariants

- `codex-rs/core/` is the business-logic center; UI crates should stay thin.
- `codex-rs/protocol/` should remain mostly pure data definitions, not business logic.
- `codex-rs/app-server-protocol/` is the external contract surface for IDE/editor integrations.
- The TypeScript SDK is a process wrapper over the CLI, not a second implementation.
- Rollout files are the durable conversation record; SQLite and memories are derived/indexed state.

## Best files to read first

For orientation, start in this order:

1. `codex-rs/README.md`
2. `codex-rs/cli/src/main.rs`
3. `codex-rs/tui/src/lib.rs`
4. `codex-rs/core/src/thread_manager.rs`
5. `codex-rs/core/src/codex_thread.rs`
6. `codex-rs/core/src/codex.rs`
7. `codex-rs/core/src/client.rs`
8. `codex-rs/app-server/src/message_processor.rs`
9. `codex-rs/app-server/src/codex_message_processor.rs`
10. `codex-rs/protocol/src/protocol.rs`
