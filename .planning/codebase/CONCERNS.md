# Repository Concerns

This document is a practical reference for the parts of the repository that are most likely to cause regressions, slow reviews, or operational surprises.

## Highest-Risk Areas

- `codex-rs/app-server/src/codex_message_processor.rs`, `codex-rs/core/src/codex.rs`, `codex-rs/tui/src/app.rs`, `codex-rs/tui/src/chatwidget.rs`, and `codex-rs/tui/src/bottom_pane/chat_composer.rs` are oversized orchestration files. They combine transport, state, business rules, UI behavior, and edge-case handling in single modules, which makes small changes hard to localize and raises merge-conflict risk.
- `codex-rs/core/src/rollout/recorder.rs`, `codex-rs/state/src/lib.rs`, `codex-rs/state/src/model/thread_metadata.rs`, and `codex-rs/app-server/src/codex_message_processor.rs` still operate across both rollout files and SQLite metadata. That dual-source model is the main persistence fragility in the repo.
- `codex-rs/linux-sandbox/src/linux_run_main.rs`, `codex-rs/linux-sandbox/src/bwrap.rs`, `codex-rs/linux-sandbox/src/landlock.rs`, `codex-rs/linux-sandbox/src/proxy_routing.rs`, `codex-rs/process-hardening/src/lib.rs`, and `codex-rs/shell-escalation/README.md` sit on the security boundary and use low-level OS behavior. Bugs here are more likely to become escape, denial-of-service, or platform-specific breakage than normal feature regressions.
- Packaging and release logic is duplicated across `codex-cli/bin/codex.js`, `sdk/typescript/src/exec.ts`, `codex-cli/scripts/build_npm_package.py`, and `.github/workflows/ci.yml`. Version and target skew is a realistic maintenance failure mode.

## Detailed Concerns

### 1. Monolithic Runtime Orchestrators

- `codex-rs/app-server/src/codex_message_processor.rs` is a major risk concentration point. It handles JSON-RPC dispatch, thread lifecycle, tool bridging, auth flows, config editing, plugin integration, rollout loading, and API version behavior in one file that is more than 8k lines long.
- `codex-rs/core/src/codex.rs` is the equivalent risk on the core side. It mixes session lifecycle, compaction, approvals, hooks, MCP behavior, network proxy wiring, realtime conversation support, and event streaming.
- `codex-rs/tui/src/app.rs`, `codex-rs/tui/src/chatwidget.rs`, and `codex-rs/tui/src/bottom_pane/chat_composer.rs` are similarly large UI coordinators. They make review difficult because rendering logic, state transitions, async coordination, and recovery behavior are interleaved.
- `codex-rs/app-server-protocol/src/protocol/v2.rs` is also large enough that API changes are easy to under-review, especially when a behavioral change also requires matching edits in `codex-rs/app-server/README.md` and the generated schema fixtures under `codex-rs/app-server-protocol/schema/`.
- Practical risk: these files are change magnets. Refactors here are expensive, selective test runs are slow, and reviewers have to reason about too many responsibilities at once.

### 2. Persistence Split Between Rollout Files and SQLite

- `codex-rs/core/src/rollout/recorder.rs` explicitly uses filesystem-first listing with DB fallback and overfetches pages so it can repair stale metadata. That is a sign the storage model is still reconciling two competing sources of truth rather than relying on one canonical store.
- `codex-rs/app-server/src/codex_message_processor.rs` still contains archive/unarchive TODOs saying the flow should mostly be rewritten using SQLite. The current implementation still locates files on disk, renames them, and then reconciles metadata.
- `codex-rs/state/src/model/thread_metadata.rs` stores rollout paths in the database, so metadata freshness depends on file moves and reconciliation happening correctly.
- Likely bug class: stale previews, thread list/read mismatch, archive state drift, and subtle failures when a rollout path exists in one place but metadata has not caught up.
- Performance risk: thread listing, summary reads, and turn reconstruction still touch rollout files directly via `read_summary_from_rollout`, `read_rollout_items_from_rollout`, and `RolloutRecorder::get_rollout_history` in `codex-rs/app-server/src/codex_message_processor.rs`.

### 3. TUI Eventing and Replay Are Fragile

- `codex-rs/tui/src/tui/event_stream.rs` explicitly warns that multiple `TuiEventStream` instances can exist but only one should be polled at a time or one instance can steal input events from another. That is a brittle runtime invariant rather than a strongly enforced design.
- `codex-rs/tui/src/app.rs` keeps a large per-thread event channel (`THREAD_EVENT_CHANNEL_CAPACITY = 32768`) and, when that fills, spawns a background task per queued send instead of blocking the UI. That avoids freezing the UI, but under sustained backlog it can create task churn and memory pressure.
- `codex-rs/tui/src/chatwidget/agent.rs` and related app event plumbing use unbounded channels for user ops and event forwarding. Those are fine for normal interactive rates, but they remove a hard stop when a producer misbehaves or a consumer stalls.
- `codex-rs/tui/src/app/pending_interactive_replay.rs` and replay logic in `codex-rs/tui/src/app.rs` add another fragile layer: restoring pending approvals, queued user input, and replay-only states across thread switches is hard to reason about and easy to break with unrelated UI changes.
- Practical risk: bugs here will look intermittent. They are more likely to show up as lost keystrokes, duplicated events, stale overlays, or thread-switch replay glitches than as deterministic crashes.

### 4. Unbounded or Loosely Bounded Async Pipelines

- `codex-rs/core/src/file_watcher.rs` bridges `notify` callbacks into Tokio with an unbounded channel. If the watcher produces a burst or the async loop stalls, memory growth is limited only by process memory.
- `codex-rs/app-server/src/thread_state.rs` uses an unbounded command channel for thread listener commands. That is reasonable for low volume, but it assumes command generation stays disciplined.
- `codex-rs/tui/src/chatwidget/agent.rs`, `codex-rs/tui/src/app_event_sender.rs`, and `codex-rs/tui/src/tui/frame_requester.rs` also rely on unbounded channels in user-facing paths.
- `codex-rs/core/src/file_watcher.rs` silently degrades if no Tokio runtime is available and only logs a warning that the watcher loop was skipped. In any path where that warning is missed, skill reload behavior can fail without a direct user-visible error.
- Practical risk: these are not obvious correctness bugs during normal use, but they reduce backpressure guarantees and make worst-case behavior harder to predict.

### 5. Linux Sandboxing Is in a Transitional, Complex State

- `codex-rs/linux-sandbox/README.md` describes a temporary feature flag (`use_linux_sandbox_bwrap`) and coexistence between the legacy Landlock path and the bubblewrap path. Transitional security code is usually the least stable security code.
- `codex-rs/linux-sandbox/src/linux_run_main.rs` contains multiple execution modes: legacy Landlock, bwrap outer stage, inner seccomp stage, and proxy-routed mode. The code is handling compatibility plus migration at the same time.
- `codex-rs/linux-sandbox/src/proxy_routing.rs` and `codex-rs/linux-sandbox/src/vendored_bwrap.rs` rely on `fork`, raw file descriptors, and `unsafe` behavior. Those are expected at this layer, but they are difficult to test exhaustively across distros and container environments.
- `codex-rs/process-hardening/src/lib.rs` includes platform-specific pre-main hardening and a Windows TODO. That means security behavior is intentionally asymmetric across platforms.
- Practical risk: sandbox bugs may only appear on particular kernels, container setups, or target triples, and they are expensive to reproduce locally.

### 6. Shell Escalation Depends on Patched Upstream Shells

- `codex-rs/shell-escalation/README.md` documents a maintained patch against upstream Bash, and `shell-tool-mcp/patches/bash-exec-wrapper.patch` plus `shell-tool-mcp/patches/zsh-exec-wrapper.patch` carry equivalent packaging debt on the npm side.
- `shell-tool-mcp/README.md` explicitly says the MCP server is experimental and the CLI version must match the MCP server version. That is a direct version-coupling warning.
- `shell-tool-mcp/src/bashSelection.ts` and `shell-tool-mcp/src/osRelease.ts` choose bundled shell binaries using `/etc/os-release` heuristics on Linux and Darwin major versions on macOS. New distros, derivative distros, and missing or unusual `os-release` data can force fallback behavior.
- Likely bug class: wrong shell variant selected, patched wrapper behavior drifting from the Rust escalation protocol, or release artifacts shipping a wrapper that no longer matches the CLI assumptions.

### 7. Network Proxy and App-Server Remote Boundaries Need Care

- `codex-rs/network-proxy/README.md` already documents limitations such as DNS rebinding being hard to prevent completely. The proxy is security-sensitive and its own docs already state that lower-layer controls may still be needed.
- `codex-rs/network-proxy/src/runtime.rs` implements allow/deny policy, DNS lookup checks, blocked-request retention, optional MITM state, and config reload behavior. That is a lot of security policy in one runtime path.
- `codex-rs/app-server/README.md` says websocket transport is experimental and unsupported for production. `codex-rs/app-server/src/transport.rs` also prints a warning that raw WS should sit behind TLS/auth if exposed remotely.
- Practical risk: operators may still treat these paths as production-safe because they exist and mostly work. The codebase contains the warning signs, but the boundary is still easy to misuse.

### 8. MCP Tool Qualification Has Collision and Naming Risk

- `codex-rs/core/src/mcp_connection_manager.rs` has to sanitize user-controlled server/tool names into Responses API-safe names, enforce a length limit, and skip duplicates. That means two distinct upstream tool names can collapse into one exposed name after sanitization.
- The duplicate-handling behavior is “warn and skip”, not “surface a hard configuration error”. That creates confusing partial availability when collisions happen.
- The same module also owns tool-cache keying for Codex apps and startup/list timing, so a change in auth state, naming, or cache version can ripple across discovery behavior.
- Practical risk: tool disappearance, hard-to-explain cache misses, and bug reports that only reproduce with certain server naming conventions.

### 9. Release Packaging and Wrapper Logic Is Duplicated

- `codex-cli/bin/codex.js` and `sdk/typescript/src/exec.ts` both encode target triple detection and platform package lookup logic instead of sharing one implementation.
- `codex-cli/scripts/build_npm_package.py` contains a third copy of platform packaging knowledge, including npm names, target triples, and which binaries belong in which packages.
- `.github/workflows/ci.yml` hardcodes `CODEX_VERSION=0.74.0` for package staging. That is a concrete drift hazard if the release process changes but the workflow is not updated.
- The repo also advertises different Node baselines in different places: `package.json` requires Node 22+, `sdk/typescript/package.json` and `shell-tool-mcp/package.json` require Node 18+, and `codex-cli/package.json` still says Node 16+.
- Practical risk: release breakage that does not come from Rust code at all, but from mismatched wrapper assumptions, outdated staging config, or engine/version skew.

### 10. Test Surface Is Uneven Across the Workspace

- `codex-rs/core/tests`, `codex-rs/app-server/tests`, and `codex-rs/tui` have substantial coverage, but many lower-level or platform-specific crates do not have visible dedicated integration-test directories, including `codex-rs/process-hardening`, `codex-rs/state`, `codex-rs/shell-escalation`, `codex-rs/protocol`, `codex-rs/responses-api-proxy`, and `codex-rs/skills`.
- `codex-rs/tui/src/chatwidget/tests.rs` is itself extremely large, which suggests the UI test surface is catching a lot of behavior but is also becoming expensive to understand and maintain.
- Snapshot coverage in `codex-rs/tui/src/**/snapshots/` is useful for visual regressions, but snapshot-heavy suites are weaker at catching timing, concurrency, and resource-pressure bugs than they are at catching render output drift.
- Practical risk: the repo tests visible behavior well in some areas, but lower-level platform behavior and cross-layer integration can still regress without a small focused failing test.

## Watchlist For Future Changes

- Any edit touching both `codex-rs/core/src/rollout/recorder.rs` and `codex-rs/app-server/src/codex_message_processor.rs` should be treated as a persistence migration, not a routine feature change.
- Any edit touching `codex-rs/linux-sandbox/src/*`, `codex-rs/process-hardening/src/lib.rs`, or `codex-rs/shell-escalation/*` should be reviewed as security-sensitive and platform-sensitive.
- Any edit touching `codex-cli/bin/codex.js`, `sdk/typescript/src/exec.ts`, or `codex-cli/scripts/build_npm_package.py` should trigger a release-wrapper consistency check.
- Any edit touching `codex-rs/tui/src/app.rs`, `codex-rs/tui/src/tui/event_stream.rs`, or `codex-rs/tui/src/app/pending_interactive_replay.rs` should assume hidden concurrency/replay regressions are possible even if the code change looks local.
