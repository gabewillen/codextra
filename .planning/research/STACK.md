# Stack Research

**Domain:** Brownfield async rolling auto-compaction for Codex Rust core + TUI + app-server
**Researched:** 2026-03-09
**Confidence:** HIGH

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| Rust workspace crates (`codex-core`, `codex-protocol`, `codex-app-server-protocol`, `codex-tui`) | workspace `0.0.0`, edition 2024 | Keep compaction logic, protocol typing, and UI behavior in the repo's existing product layers | The feature is a session/history mutation problem, and this repo already centralizes that in `codex-rs/core/src/codex.rs`, `codex-rs/core/src/compact.rs`, `codex-rs/core/src/compact_remote.rs`, `codex-rs/protocol/src/protocol.rs`, and `codex-rs/tui/src/chatwidget.rs`. |
| `tokio` + `tokio-util::sync::CancellationToken` | `tokio = 1`, `tokio-util = 0.7.18` | Run background compactions without blocking the active turn and support cancellation/fallback | This repo already uses `tokio::spawn`, task lifecycles, and cancellation tokens in `codex-rs/core/src/tasks/mod.rs`, `codex-rs/core/src/tasks/ghost_snapshot.rs`, and `codex-rs/core/src/codex.rs`. Reusing that model avoids introducing a second async runtime or job system. |
| Existing compaction engines (`compact.rs` local, `compact_remote.rs` remote) | in-repo | Preserve current local/remote summarization behavior while changing only orchestration | The actual summarization and history replacement logic already exists and is tested. The brownfield move is to wrap these engines in async orchestration attached near `run_auto_compact` in `codex-rs/core/src/codex.rs`, not replace them. |
| Existing model transport stack (`reqwest`, `eventsource-stream`, `tokio-tungstenite`) | `reqwest = 0.12`, `eventsource-stream = 0.2.3`, patched `tokio-tungstenite = 0.28.0` | Keep provider streaming and remote `/responses/compact` compatibility | `codex-rs/core/src/client.rs` already owns provider auth, SSE/WebSocket fallback, and compact endpoint calls. Async auto-compaction should call through that same client stack so retries, auth, and telemetry remain consistent. |
| Existing terminal/UI stack (`ratatui`, `crossterm`) | patched `ratatui = 0.29.0`, patched `crossterm = 0.28.1` | Render a lightweight "compacting" indicator below the composer without transcript churn | The TUI already has the right primitives in `codex-rs/tui/src/status_indicator_widget.rs`, `codex-rs/tui/src/bottom_pane/footer.rs`, and `codex-rs/tui/src/chatwidget.rs`. This is a UI state addition, not a new frontend framework decision. |
| Typed protocol/export stack (`serde`, `schemars`, `ts-rs`) | `serde = 1`, `schemars = 0.8.22`, `ts-rs = 11` | Add durable cross-client compaction status if the indicator needs stable app-server support | If this feature graduates beyond TUI-only status text, `codex-rs/app-server-protocol/src/protocol/common.rs` and `codex-rs/app-server-protocol/src/protocol/v2.rs` are already the repo-standard way to expose typed notifications and schema. |

### Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `tracing` | `0.1.44` | Instrument compaction spawn/start/finish/fallback and correlate overlapping jobs | Use in `codex-rs/core/src/codex.rs` and compaction modules for per-job IDs, transcript-range metadata, and fallback reason logging. |
| `tokio::task::JoinSet` or existing per-task `JoinHandle` patterns | bundled with `tokio = 1` | Track multiple in-flight compactions cleanly | Use only if session state needs to own more than one compaction job concurrently. `codex-rs/core/src/mcp_connection_manager.rs` shows an existing `JoinSet` pattern; `codex-rs/core/src/tasks/mod.rs` shows the existing `JoinHandle`/cancellation pattern. |
| `uuid` | `1` | Stable compaction operation IDs for UI/app-server correlation | Use if background compactions need durable IDs separate from turn IDs, especially once multiple transcript ranges can compact concurrently. |
| `pretty_assertions`, `wiremock`, `insta` | workspace | Validate splice behavior, provider behavior, and UI indicator output | Use `wiremock` and existing core/app-server compaction tests under `codex-rs/core/tests/suite/compact.rs` and `codex-rs/app-server/tests/suite/v2/compaction.rs`; use `insta` for any TUI indicator change. |

### Development Tools

| Tool | Purpose | Notes |
|------|---------|-------|
| `just fmt` | Workspace formatting | Already standard for Rust changes in `codex-rs/`; no stack changes required. |
| `cargo test -p codex-core` / `cargo test -p codex-tui` / `cargo test -p codex-app-server` | Validate core splice logic, TUI indicator rendering, and app-server notifications | Prefer targeted crate tests first because the feature spans core plus client surfaces. |
| `cargo insta` | Review TUI snapshot deltas | Required if the indicator changes any user-visible rendering in `codex-rs/tui/`. |
| `just fix -p <crate>` | Clippy cleanup for the touched crate | Practical for final cleanup because the feature will likely touch lint-sensitive async control flow. |

## Installation

```bash
# Recommended stack change for this feature: no new runtime/framework installation.
# Reuse the workspace crates already pinned in codex-rs/Cargo.toml.

cd codex-rs
just fmt
cargo test -p codex-core
cargo test -p codex-tui
cargo test -p codex-app-server
```

## Recommended Attachment Map

- Core orchestration: attach async rolling auto-compaction near `run_auto_compact`, `run_pre_sampling_compact`, and the post-sampling loop in `codex-rs/core/src/codex.rs`.
- Local summarization path: keep using `codex-rs/core/src/compact.rs`.
- Remote summarization path: keep using `codex-rs/core/src/compact_remote.rs` and `codex-rs/core/src/client.rs`.
- History replacement/splice ownership: keep session history mutation in `codex-rs/core/src/codex.rs` near `replace_compacted_history`.
- Background-task pattern reference: model the lifecycle after `codex-rs/core/src/tasks/mod.rs` and `codex-rs/core/src/tasks/ghost_snapshot.rs`.
- Typed event surface, if added: extend `codex-rs/protocol/src/protocol.rs`, then map through `codex-rs/app-server-protocol/src/protocol/common.rs` and `codex-rs/app-server-protocol/src/protocol/v2.rs`.
- TUI indicator state: integrate through `codex-rs/tui/src/chatwidget.rs`, rendered by `codex-rs/tui/src/status_indicator_widget.rs` and/or `codex-rs/tui/src/bottom_pane/footer.rs`.

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| Tokio background jobs attached to session state | New actor framework or external job queue | Only use an actor-style refactor if compaction becomes a repo-wide generalized background service. That is larger than this feature needs. |
| Reuse `compact.rs` / `compact_remote.rs` engines | Replace compaction with a brand-new summarizer pipeline | Only use a new pipeline if the existing compaction outputs are functionally wrong. Current evidence says orchestration is the gap, not summarization capability. |
| Typed protocol notification for durable cross-client state | TUI-only string status via `BackgroundEvent` | Use the string path only for the smallest v1 UI experiment. Once app-server clients need exact counts, IDs, or states, promote the data to typed protocol. |
| Session-owned handles plus transcript-range metadata | Global singleton compaction manager | Use a global manager only if compactions must coordinate across threads. The requirement is per-thread rolling compaction, so session-local ownership is simpler and safer. |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| A new async runtime such as `async-std` or `actix` | The workspace already standardizes on Tokio; mixing runtimes raises cancellation, testing, and instrumentation cost | `tokio`, `tokio::spawn`, `CancellationToken`, and existing session-task patterns |
| A new persistence layer or SQL queue just for compaction bookkeeping | The transcript is already the durable source of truth, and compaction replacement already persists through rollout items | Session-local in-memory tracking plus existing rollout persistence in `codex-rs/core/src/codex.rs` |
| Using `BackgroundEventEvent.message` as the long-term data contract | It is an untyped string, currently used by TUI as a generic status header in `codex-rs/tui/src/chatwidget.rs` | Typed protocol/event fields once clients need stable parsing or richer state |
| Rewriting the TUI indicator as transcript messages | The project explicitly wants uninterrupted agent interaction and a lightweight indicator below input | Status/footer rendering in `codex-rs/tui/src/status_indicator_widget.rs` and `codex-rs/tui/src/bottom_pane/footer.rs` |
| Replacing manual compaction and pre-turn compaction semantics in the same change | The project scope is automatic mid-turn compaction only; widening scope increases regression risk | Keep manual/pre-turn flows on their existing paths and isolate async behavior to auto mid-turn compaction |

## Stack Patterns by Variant

**If the provider is OpenAI / remote compaction-capable:**
- Keep `codex-rs/core/src/compact_remote.rs` as the execution engine.
- Reuse `codex-rs/core/src/client.rs::compact_conversation_history` so auth, retries, and telemetry stay aligned with the rest of the provider stack.

**If the provider is local / non-OpenAI:**
- Keep `codex-rs/core/src/compact.rs` as the execution engine.
- Run it in the same async orchestration layer so only the compaction worker differs, not the session/UI state model.

**If the feature needs only a minimal TUI indicator first:**
- Start with existing status-indicator plumbing in `codex-rs/tui/src/chatwidget.rs`.
- Use this only as a narrow bootstrap because `BackgroundEvent` is not a good long-term cross-client contract.

**If multiple compactions must overlap on different transcript ranges:**
- Track a per-thread set of jobs keyed by compaction operation ID and transcript slice metadata.
- Prefer Tokio-native tracking (`JoinSet` or owned `JoinHandle` + `CancellationToken`) over adding a separate scheduling library.

## Version Compatibility

| Package A | Compatible With | Notes |
|-----------|-----------------|-------|
| `tokio@1` | `tokio-util@0.7.18` | Already paired in the workspace; use this for cancellation and background task ownership. |
| `reqwest@0.12` | `eventsource-stream@0.2.3` | Matches the existing SSE client flow in `codex-rs/core/src/client.rs`. |
| patched `tokio-tungstenite@0.28.0` | `tokio@1` | Keep the workspace-patched revision from `codex-rs/Cargo.toml`; do not swap websocket stacks for this feature. |
| patched `ratatui@0.29.0` | patched `crossterm@0.28.1` | Preserve the repo's patched pair because the TUI already depends on those patches. |
| `serde@1` | `schemars@0.8.22`, `ts-rs@11` | This is the existing path for app-server protocol shape and schema export. |

## Practical Recommendation

Build this feature on the current Rust/Tokio/session stack with no new framework dependencies. The practical stack choice is: session-owned Tokio background jobs in `codex-rs/core/src/codex.rs`, current compaction engines in `codex-rs/core/src/compact.rs` and `codex-rs/core/src/compact_remote.rs`, typed protocol only if the indicator must be durable across app-server clients, and Ratatui/Crossterm rendering for the below-input indicator.

The main brownfield constraint is not library capability; it is keeping transcript integrity while multiple compactions race with new messages. That means the highest-value stack choices are cancellation-aware Tokio job ownership, explicit operation IDs/slice metadata, and reusing the existing history replacement and rollout persistence paths instead of adding new infrastructure.

## Sources

- Local repo: `codex-rs/Cargo.toml` - workspace versions and patched crate constraints.
- Local repo: `codex-rs/core/src/codex.rs` - current inline auto-compaction trigger points and history replacement ownership.
- Local repo: `codex-rs/core/src/compact.rs` - local compaction engine and replacement-history building.
- Local repo: `codex-rs/core/src/compact_remote.rs` - remote compaction engine and post-processing.
- Local repo: `codex-rs/core/src/tasks/mod.rs` and `codex-rs/core/src/tasks/ghost_snapshot.rs` - existing background-task and cancellation patterns.
- Local repo: `codex-rs/protocol/src/protocol.rs` - event types including `BackgroundEvent` and compaction lifecycle items.
- Local repo: `codex-rs/app-server-protocol/src/protocol/common.rs` and `codex-rs/app-server-protocol/src/protocol/v2.rs` - typed app-server notification surface.
- Local repo: `codex-rs/tui/src/chatwidget.rs`, `codex-rs/tui/src/status_indicator_widget.rs`, and `codex-rs/tui/src/bottom_pane/footer.rs` - current status-indicator and footer rendering hooks.
- Local repo: `codex-rs/core/tests/suite/compact.rs` and `codex-rs/app-server/tests/suite/v2/compaction.rs` - current compaction regression coverage.
- Tokio `CancellationToken` docs: https://docs.rs/tokio-util/latest/tokio_util/sync/struct.CancellationToken.html
- Tokio `JoinSet` docs: https://docs.rs/tokio/latest/tokio/task/struct.JoinSet.html
- Tracing instrumentation docs: https://docs.rs/tracing/latest/tracing/trait.Instrument.html
- Ratatui `Stylize` docs: https://docs.rs/ratatui/latest/ratatui/style/trait.Stylize.html
- `ts-rs` docs: https://docs.rs/ts-rs/latest/ts_rs/
- `schemars` docs: https://docs.rs/schemars/latest/schemars/
- `reqwest` docs: https://docs.rs/reqwest/latest/reqwest/
- `eventsource-stream` docs: https://docs.rs/eventsource-stream/latest/eventsource_stream/

---
*Stack research for: async rolling auto-compaction in Codex*
*Researched: 2026-03-09*
