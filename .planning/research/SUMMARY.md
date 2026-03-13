# Project Research Summary

**Project:** Codex async rolling auto-compaction
**Domain:** Brownfield Rust thread/session runtime enhancement
**Researched:** 2026-03-09
**Confidence:** HIGH

## Executive Summary

This project is a brownfield runtime change inside Codex, not a new subsystem. Research across stack, features, architecture, and pitfalls converges on one recommendation: keep compaction ownership in `codex-rs/core/src/codex.rs`, refactor the existing local and remote compaction paths into snapshot-based workers, and add a commit layer that can safely splice a compacted range back into live history after new messages have arrived.

The roadmap implication is that correctness work has to come before UX polish or true rolling concurrency. The first milestone should establish range-safe background compaction, strict failure fallback to the existing blocking path, and durable replay semantics. Only after transcript integrity and recovery are proven should the project widen into richer protocol status, TUI indicator behavior, and multi-compaction overlap.

The biggest risks are transcript corruption from stale compaction results, drift between live session state and persisted rollout/SQLite state, and split-brain recovery when a background compaction fails while the agent is still running. The safest mitigation is a phased plan that separates: core state machine and splice validation, persistence/replay parity, client visibility, then concurrency hardening.

## Key Findings

### Recommended Stack

The research does not support introducing new infrastructure. The existing Rust workspace, Tokio task model, current compaction engines, and current TUI/app-server surfaces are already the right stack for this feature. The work is primarily orchestration and state management around existing code, not a library selection problem.

**Core technologies:**
- `codex-core` plus existing session/thread architecture: own trigger policy, job coordination, splice commit, and fallback because history semantics already live there.
- `tokio` plus existing cancellation/task patterns: run background compactions without blocking the active turn and keep failure/fallback cancellation consistent with the rest of the runtime.
- `compact.rs` and `compact_remote.rs`: keep current summarization behavior, but refactor them to return snapshot-derived patch data instead of directly replacing live history.
- Existing protocol and app-server layers: preserve cross-client behavior and only add typed lifecycle state if the TUI-only status path proves too weak.
- Existing TUI footer/status rendering: show compaction below the input without polluting transcript history.

### Expected Features

Launch scope is narrower than the full vision. The must-have product behavior is background mid-turn auto-compaction that does not interrupt the agent, safely replaces only the compacted slice, falls back to the current blocking path on failure, and exposes lightweight status to the user. Manual compaction and pre-turn protective compaction should remain blocking and semantically unchanged.

**Must have (table stakes):**
- Non-blocking mid-turn auto-compaction triggered from the current threshold path.
- Range-safe transcript splice that preserves messages created after compaction started.
- Failure fallback that stops the affected turn and reuses the current blocking recovery path.
- A lightweight TUI indicator below the input while compaction is active.
- Cross-surface parity for persisted compaction results and existing `ContextCompaction` items.

**Should have after core correctness is proven:**
- Multiple background compactions on disjoint ranges.
- More informative status state for TUI and app-server clients.
- Better observability and diagnostics around compaction success, failure, and efficiency.

**Defer (v2+):**
- Predictive/speculative compaction heuristics.
- User-facing queue controls or policy tuning.
- Broader redesign of manual compaction or pre-turn safety compaction.

### Architecture Approach

The architecture research points to a three-stage design inside the current session runtime: a planner in `codex-rs/core/src/codex.rs` detects threshold crossings and snapshots a compactable range, a worker in `codex-rs/core/src/compact.rs` or `codex-rs/core/src/compact_remote.rs` summarizes that immutable slice in the background, and a committer under session ownership validates anchors and splices the result into live history while preserving the newer suffix. TUI and app-server layers should only observe lifecycle state; they should not infer compaction from transcript text or own job state themselves.

**Major components:**
1. Planner/coordinator in `codex-rs/core/src/codex.rs` — trigger background jobs, track state, arbitrate fallback.
2. Snapshot compaction workers in `codex-rs/core/src/compact.rs` and `codex-rs/core/src/compact_remote.rs` — summarize a frozen transcript slice only.
3. Session commit and persistence path in core rollout/session state — validate anchors, splice safely, persist replayable replacement history.
4. Client visibility layer in protocol/app-server/TUI — surface lifecycle state and render the below-input indicator.

### Critical Pitfalls

1. **Applying a summary to the wrong transcript slice** — avoid blind whole-history replacement; bind each result to a specific source range or generation and commit with compare-and-swap semantics.
2. **Live state and persisted rollout drifting apart** — keep compaction persistence append-only and ordered so replay, resume, rollback, and `thread/read` reconstruct the same post-splice history.
3. **Background failure causing split-brain recovery** — use an explicit per-turn state machine so only one terminal path wins: applied, failed, fallback running, or aborted.
4. **TUI status colliding with existing running/footer state** — model compaction separately from the main task-running indicator so the footer can show both agent activity and compaction activity.
5. **Compaction storms on long turns** — debounce scheduling by transcript generation and useful token delta before enabling true rolling overlap.

## Implications for Roadmap

Based on the research, the roadmap should treat transcript correctness as the gate for everything else. Suggested phase structure:

### Phase 1: Background Compaction Core
**Rationale:** This is the enabling phase. Without a safe snapshot/patch/commit model, any UI or concurrency work sits on unstable history semantics.
**Delivers:** Snapshot-based compaction workers, session-owned job state, generation/range anchoring, strict fallback arbitration, and auto mid-turn enqueue-and-continue behavior.
**Addresses:** Background auto-compaction, safe splice behavior, failure fallback.
**Avoids:** Wrong-slice replacement, split-brain failure handling, whole-history replacement reuse.

### Phase 2: Persistence and Replay Parity
**Rationale:** Once live behavior works, brownfield durability has to match it before the feature is safe to ship across resume, rollback, thread list, and app-server read paths.
**Delivers:** Replayable compaction checkpoints, ordered rollout persistence, metadata parity, and regression coverage for resume/fork/rollback after async compaction.
**Uses:** Existing rollout recorder, session state, reconstruction paths, and app-server thread history handling.
**Implements:** Commit durability and reconstruction semantics rather than new product behavior.

### Phase 3: Protocol and TUI Visibility
**Rationale:** User-visible status should land only after core and persistence semantics are trustworthy, otherwise the UI will describe behavior that is still shifting.
**Delivers:** Lifecycle status surface, TUI below-input indicator, and app-server pass-through if typed state is needed beyond current generic background messaging.
**Uses:** Existing footer/status rendering and current `ContextCompaction` lifecycle coverage.
**Implements:** Observable background compaction without transcript churn.

### Phase 4: Rolling Concurrency and Hardening
**Rationale:** Multiple in-flight compactions are explicitly desired, but the research treats them as a follow-on once single-job anchoring and replay are proven.
**Delivers:** Disjoint-range overlap support, debounce/scheduling controls, out-of-order completion handling, and expanded race/performance tests.
**Uses:** Tokio-native job tracking and the splice model proven in earlier phases.
**Implements:** True rolling auto-compaction at scale rather than single background compaction.

### Phase Ordering Rationale

- Phase 1 comes first because every later concern depends on a correct splice contract between frozen snapshots and live session history.
- Phase 2 follows immediately because this is a brownfield thread engine; shipping live-only correctness without replay parity would break resume, rollback, and app-server reads.
- Phase 3 is intentionally after persistence work because the indicator is presentation, not the core risk driver.
- Phase 4 is last because concurrent overlapping jobs amplify every race called out in the research and should build on a validated single-job model.

### Research Flags

Phases likely needing deeper research during planning:
- **Phase 2:** Persistence ordering and replay semantics across rollout JSONL, SQLite metadata, rollback, and thread resume are the least forgiving integration area.
- **Phase 3:** If generic `BackgroundEvent` is insufficient, protocol shape and cross-client compatibility need a tighter app-server API decision.
- **Phase 4:** Overlapping-range scheduling and out-of-order completion policy need explicit design validation before implementation.

Phases with standard patterns (skip research-phase):
- **Phase 1:** The core seams are already identified and the needed pattern is established: Tokio background work plus session-owned commit/fallback control.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | Research consistently points to reusing the existing Rust/Tokio/session stack with no new framework decisions. |
| Features | HIGH | Scope boundaries and launch priorities are explicit in `.planning/PROJECT.md` and reinforced by feature research. |
| Architecture | HIGH | The integration seams in core, protocol, app-server, and TUI are concrete and already visible in the repo. |
| Pitfalls | HIGH | The failure modes are specific to current code paths and existing persistence/replay behavior, not generic guesses. |

**Overall confidence:** HIGH

### Gaps to Address

- The exact metadata shape for a replayable async compaction checkpoint still needs phase-planning detail; the summary only establishes the requirement.
- The threshold/debounce policy for repeated long-turn compactions should be validated with representative tests before Phase 4 broadens concurrency.
- It is still an implementation decision whether TUI can stay on generic background messaging or needs a typed protocol/app-server event for durable state.

## Sources

### Research docs

- `.planning/PROJECT.md`
- `.planning/research/STACK.md`
- `.planning/research/FEATURES.md`
- `.planning/research/ARCHITECTURE.md`
- `.planning/research/PITFALLS.md`

### Key repo files checked

- `codex-rs/core/src/codex.rs`
- `codex-rs/core/src/compact.rs`
- `codex-rs/core/src/compact_remote.rs`
- `codex-rs/protocol/src/protocol.rs`
- `codex-rs/tui/src/chatwidget.rs`
- `codex-rs/tui/src/bottom_pane/footer.rs`
- `codex-rs/app-server/tests/suite/v2/compaction.rs`

---
*Research completed: 2026-03-09*
*Ready for roadmap: yes*
