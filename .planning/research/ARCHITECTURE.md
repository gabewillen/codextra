# Architecture Research

**Domain:** Brownfield rolling auto-compaction in Codex thread/session runtime
**Researched:** 2026-03-09
**Confidence:** HIGH

## Recommended Architecture

### System Overview

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ Frontends                                                                   │
│ `codex-rs/tui/`  `codex-rs/exec/`  `codex-rs/app-server/`                   │
└──────────────────────────────┬───────────────────────────────────────────────┘
                               │ `Op` / `Event`
┌──────────────────────────────▼───────────────────────────────────────────────┐
│ Thread Runtime                                                              │
│ `codex-rs/core/src/thread_manager.rs`                                       │
│ `codex-rs/core/src/codex_thread.rs`                                         │
│ Thin thread/session entrypoints only                                        │
└──────────────────────────────┬───────────────────────────────────────────────┘
                               │
┌──────────────────────────────▼───────────────────────────────────────────────┐
│ Turn Executor + Session                                                     │
│ `codex-rs/core/src/codex.rs`                                                │
│ - normal sampling loop                                                      │
│ - auto-compaction trigger/planner                                           │
│ - session-owned compaction coordinator                                      │
└───────────────┬───────────────────────────────────────────────┬──────────────┘
                │ immutable snapshot                             │ validated splice
┌───────────────▼──────────────────────┐      ┌──────────────────▼─────────────┐
│ Compaction Workers                   │      │ Session History Committer       │
│ `codex-rs/core/src/compact.rs`       │      │ `codex-rs/core/src/state/`      │
│ `codex-rs/core/src/compact_remote.rs`│      │ `ContextManager` + rollout      │
│ summarize a bounded transcript slice │      │ replace slice, recompute usage  │
└───────────────┬──────────────────────┘      └──────────────────┬─────────────┘
                │                                                 │
┌───────────────▼──────────────────────┐      ┌──────────────────▼─────────────┐
│ Durable Replay                       │      │ UI / API Status                 │
│ `RolloutItem::Compacted` with full   │      │ structured compaction events    │
│ `replacement_history` stays source   │      │ TUI footer indicator below input│
│ of truth for resume/fork             │      │ app-server pass-through         │
└──────────────────────────────────────┘      └────────────────────────────────┘
```

### Core Recommendation

Keep rolling auto-compaction inside the existing `Session` and `run_turn` flow in `codex-rs/core/src/codex.rs`. Do not create a new thread type, a TUI-owned worker, or a sidecar persistence service.

The main change is to split compaction into three explicit phases:

1. `Planner`: detect threshold crossings in `run_turn`, choose a compactable transcript slice, and enqueue a background job against an immutable snapshot.
2. `Worker`: run the existing local or remote summarization logic over that frozen slice in `codex-rs/core/src/compact.rs` or `codex-rs/core/src/compact_remote.rs`.
3. `Committer`: under the session lock, validate that the slice anchor still matches, splice the compacted segment into live history, persist a new `RolloutItem::Compacted`, and emit state events.

That keeps the current architecture intact:

- `ThreadManager` in `codex-rs/core/src/thread_manager.rs` still only creates and tracks threads.
- `CodexThread` in `codex-rs/core/src/codex_thread.rs` stays a conduit.
- `SessionState` in `codex-rs/core/src/state/session.rs` remains the owner of mutable history.
- Resume/fork replay in `codex-rs/core/src/codex/rollout_reconstruction.rs` continues to rebuild from persisted replacement history instead of learning a second history format.

## Component Boundaries

| Component | Owns | Should not own |
| --- | --- | --- |
| Auto-compaction planner in `codex-rs/core/src/codex.rs` | Trigger policy, job creation, fallback decision | Transcript mutation details, UI formatting |
| Session compaction coordinator in `codex-rs/core/src/codex.rs` plus `codex-rs/core/src/state/session.rs` | In-flight job registry, range reservation, commit serialization, failure state | Model-specific summarization logic |
| Compaction workers in `codex-rs/core/src/compact.rs` and `codex-rs/core/src/compact_remote.rs` | Pure summarization of a bounded snapshot | Choosing live ranges, touching session state while awaiting network |
| History committer via `ContextManager` and `SessionState::replace_history` | Splice validation, token recomputation, rollout persistence | Trigger policy |
| TUI state in `codex-rs/tui/src/chatwidget.rs` and `codex-rs/tui/src/bottom_pane/` | Rendering active compaction count/status below the input | Inferring compaction from transcript text |
| App-server adapters in `codex-rs/app-server/` and `codex-rs/app-server-protocol/` | Pass-through notification surface | Core compaction orchestration |

## Data Flow

### Rolling Compaction Flow

```text
`run_turn()` in `codex-rs/core/src/codex.rs`
    ↓
token threshold reached after sampling and follow-up still needed
    ↓
planner snapshots `ContextManager` and reserves a compactable range
    ↓
background worker runs local (`compact.rs`) or remote (`compact_remote.rs`) summarization
    ↓
worker returns `CompactionPatch { range, summary, checkpoint_metadata }`
    ↓
session lock validates anchors against current history
    ↓
splice compacted range into live history, preserve newer suffix unchanged
    ↓
persist full `replacement_history` through `RolloutItem::Compacted`
    ↓
emit structured compaction state event + completion marker
    ↓
TUI footer/app-server clients update indicator state
```

### Failure Flow

```text
background worker fails
    ↓
session marks recovery-required and emits failure state
    ↓
current turn is interrupted through existing turn cancellation path
    ↓
core re-enters the existing blocking compaction path
    ↓
`run_auto_compact()` / `run_remote_compact_task()` completes recovery
    ↓
normal turn processing may resume
```

## Transcript Shape Recommendation

The rolling feature should compact a bounded older slice and preserve the newer suffix verbatim. That means the current whole-history helpers in `codex-rs/core/src/compact.rs` are the wrong final abstraction for background mid-turn use.

Recommended output model:

- A job targets a specific slice of `ContextManager::raw_items()`, not the whole session.
- The commit step replaces only that slice with one compacted summary segment.
- Items appended after the job started remain below the new compacted segment.
- Persisted `replacement_history` should still contain the full post-splice history so `codex-rs/core/src/codex/rollout_reconstruction.rs` and app-server thread history rebuilding keep working unchanged.

Important implication: the current `InitialContextInjection::BeforeLastUserMessage` rule in `codex-rs/core/src/compact.rs` is coupled to whole-history replacement. Reusing it directly for rolling splices will create incorrect prompt shape. Rolling compaction needs a separate splice builder that knows whether the replacement is the top segment or a middle segment.

## Build Order

1. Refactor `codex-rs/core/src/compact.rs` and `codex-rs/core/src/compact_remote.rs` so summarization can run against an immutable snapshot and return a patch object instead of directly mutating session history.
2. Add session-owned in-flight job tracking in `codex-rs/core/src/codex.rs` and `codex-rs/core/src/state/session.rs`.
3. Implement splice validation and commit logic against `ContextManager`, then continue persisting full `CompactedItem.replacement_history` in `codex-rs/protocol/src/protocol.rs`.
4. Switch the automatic mid-turn path in `run_turn` from inline `run_auto_compact()` to enqueue-and-continue, while keeping pre-turn compaction and manual `Op::Compact` in `codex-rs/core/src/tasks/compact.rs` blocking.
5. Add structured compaction state events to `codex-rs/protocol/src/protocol.rs`, adapt pass-through in `codex-rs/app-server/`, and render the indicator in `codex-rs/tui/src/chatwidget.rs` plus `codex-rs/tui/src/bottom_pane/footer.rs`.
6. Extend tests in `codex-rs/core/tests/suite/compact.rs`, `codex-rs/core/tests/suite/compact_remote.rs`, `codex-rs/app-server/tests/suite/v2/compaction.rs`, and TUI snapshots around `codex-rs/tui/src/bottom_pane/footer.rs`.

## Integration Points

### Core

- `codex-rs/core/src/codex.rs`
  The trigger point is already here in `run_turn()` and `run_auto_compact()`. This file should own queueing, fallback, and turn interruption policy.
- `codex-rs/core/src/compact.rs`
  Keep local summarization logic here, but separate prompt-building/summarization from session mutation.
- `codex-rs/core/src/compact_remote.rs`
  Same boundary as local compaction. Remote compaction should return a patch, not call `replace_compacted_history()` directly.
- `codex-rs/core/src/state/session.rs`
  Add session-scoped job metadata here because compaction state is mutable session state, not thread-manager state.
- `codex-rs/core/src/codex/rollout_reconstruction.rs`
  No new replay format is needed if full post-splice `replacement_history` is still persisted.

### TUI

- `codex-rs/tui/src/chatwidget.rs`
  This currently renders `EventMsg::ContextCompacted(_)` as transcript text. Background auto-compaction should instead update indicator state and avoid transcript churn.
- `codex-rs/tui/src/bottom_pane/footer.rs`
  Best place for the lightweight below-input indicator. It already renders passive status/context information and avoids polluting transcript history.
- `codex-rs/tui/src/status_indicator_widget.rs`
  Reuse only if the compaction indicator must share the running status row. Otherwise keep compaction separate so agent work and compaction state can coexist.

### App-Server / Protocol

- `codex-rs/protocol/src/protocol.rs`
  Add a structured event for compaction lifecycle state instead of overloading `BackgroundEvent`.
- `codex-rs/app-server-protocol/src/protocol/thread_history.rs`
  Keep compaction transcript markers for persisted history, but do not make the new live status event a transcript item.
- `codex-rs/app-server/src/codex_message_processor.rs`
  Manual `thread/compact/start` stays blocking/manual. No special orchestration should move here.

## Internal Boundary Rules

| Boundary | Rule |
| --- | --- |
| `ThreadManager` -> `Codex` | No background job ownership outside the session; threads remain transport shells. |
| `run_turn` -> compaction worker | Pass immutable snapshots only. Never hand a worker live mutable history handles. |
| worker -> committer | Return structured patch metadata, not pre-applied history mutation. |
| session -> TUI/app-server | Emit structured lifecycle state; do not make UIs infer compaction from warning text or transcript cells. |
| rolling auto-compaction -> manual/pre-turn compaction | Share summarization helpers, but keep trigger semantics separate. Manual and pre-turn paths stay blocking. |

## Anti-Patterns

### 1. Reusing Whole-History Replacement for Rolling Splices

What people do:
Call `run_inline_auto_compact_task()` and then keep the suffix manually.

Why it is wrong:
The current helper assumes whole-history replacement and `BeforeLastUserMessage` context injection. That assumption does not hold once messages can arrive during compaction.

Do this instead:
Refactor the current helpers into `snapshot -> patch` logic, then let the session commit layer build the final post-splice history.

### 2. Letting the TUI Infer State from Transcript Text

What people do:
Show "Context compacted" in transcript and treat that as the user-visible status.

Why it is wrong:
The requirement is a live below-input indicator while work continues. Transcript cells are durable history, not transient job state.

Do this instead:
Emit a structured compaction lifecycle event and render it in `codex-rs/tui/src/bottom_pane/footer.rs`.

### 3. Storing In-Flight Jobs in `ThreadManager`

What people do:
Attach compaction bookkeeping to `ThreadManager` because it already tracks threads.

Why it is wrong:
Compaction state is per-session mutable runtime state tied to one `Session` history buffer. `ThreadManager` should stay a registry/factory.

Do this instead:
Store reservations, job ids, and failure flags beside `SessionState` in `codex-rs/core/src/state/session.rs`.

### 4. Falling Back Silently After Background Failure

What people do:
Log the failure and wait for the next token check.

Why it is wrong:
The project requirement is explicit: failed background compaction must interrupt active agent work and fall back to the existing blocking recovery flow.

Do this instead:
Mark recovery-required, interrupt the current turn, then run the existing blocking path before continuing.

## Recommended Brownfield Structure

No new top-level crate is needed. The cleanest brownfield change is:

- Extend `codex-rs/core/src/codex.rs` with an auto-compaction planner/coordinator.
- Refactor `codex-rs/core/src/compact.rs` and `codex-rs/core/src/compact_remote.rs` into reusable snapshot workers plus blocking adapters.
- Extend session state in `codex-rs/core/src/state/session.rs`.
- Add protocol/app-server event wiring in existing protocol files.
- Render the indicator in `codex-rs/tui/src/chatwidget.rs` and `codex-rs/tui/src/bottom_pane/footer.rs`.

This keeps the feature inside the existing Codex spine instead of creating a separate subsystem that replay, app-server, and the TUI would all need to learn independently.

## Sources

- `./.planning/PROJECT.md`
- `./.planning/codebase/ARCHITECTURE.md`
- `./.planning/codebase/STRUCTURE.md`
- `codex-rs/core/src/codex.rs`
- `codex-rs/core/src/compact.rs`
- `codex-rs/core/src/compact_remote.rs`
- `codex-rs/core/src/state/session.rs`
- `codex-rs/core/src/codex/rollout_reconstruction.rs`
- `codex-rs/core/src/tasks/compact.rs`
- `codex-rs/protocol/src/protocol.rs`
- `codex-rs/protocol/src/items.rs`
- `codex-rs/app-server/src/codex_message_processor.rs`
- `codex-rs/app-server-protocol/src/protocol/thread_history.rs`
- `codex-rs/tui/src/chatwidget.rs`
- `codex-rs/tui/src/bottom_pane/footer.rs`
- `codex-rs/tui/src/status_indicator_widget.rs`
