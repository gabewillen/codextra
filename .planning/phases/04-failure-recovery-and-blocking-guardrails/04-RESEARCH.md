# Phase 4 Research: Failure Recovery And Blocking Guardrails

## What Phase 4 Needs To Deliver

Phase 4 is the recovery and guardrail phase for async rolling compaction.

To satisfy `RECV-01`, `RECV-02`, `COMP-01`, and `COMP-02`, this phase must ensure:

- a failed background auto-compaction stops the active agent turn instead of being silently ignored
- failure recovery reuses the existing blocking compaction path instead of inventing a second summarization flow
- every background auto-compaction reaches exactly one terminal outcome: applied, failed-then-fallback, or aborted
- user-triggered manual compaction remains a blocking operation
- pre-turn protective compaction remains a blocking operation

This phase should stay narrower than later work:

- do not add below-input status UI or success/failure indicators; that is Phase 5
- do not add multi-compaction overlap; that is Phase 5
- do not redesign transcript splicing or durable replay; that was completed in Phases 2 and 3

## Current Background Auto-Compaction Behavior

### Start and worker execution

`codex-rs/core/src/codex.rs` starts mid-turn auto-compaction from `start_background_auto_compact(...)` after a follow-up-capable sampling step crosses the token limit.

Today that path:

- snapshots the current history
- emits `ItemStarted(ContextCompaction)`
- spawns a worker through `run_background_auto_compact_worker(...)`
- stores the in-flight worker in `ActiveTurn.background_auto_compaction`

The worker already supports both local and remote compaction:

- local uses `crate::compact::compute_local_compact_result(...)`
- remote uses `crate::compact_remote::run_remote_compact_worker(...)`

### Success path

Successful results are converted into `BackgroundAutoCompactionOutcome::Succeeded(...)`, moved into `completed_background_auto_compaction`, and later applied through `apply_completed_background_auto_compact_if_ready(...)`.

That apply path only consumes successful completions:

- it calls `take_successful_completed_background_auto_compaction()`
- failed completions are left stored but never applied

### Failure path

The current failure behavior is incomplete for Phase 4:

- local worker failure becomes `BackgroundAutoCompactionOutcome::Failed(String)` and is stored
- remote worker failure also emits an immediate `EventMsg::Error(...)` before the failed outcome is stored
- neither path currently interrupts the active agent turn
- neither path triggers blocking fallback compaction

### Cleanup and loss of failed state

`codex-rs/core/src/tasks/mod.rs` currently clears both in-flight and completed background compaction state when the active turn finishes or all tasks are aborted:

- `on_task_finished(...)` takes any in-flight background compaction, clears completed background compaction state, and cancels the worker if needed
- `take_all_running_tasks(...)` does the same during interruption teardown

Planning implication:

- Phase 4 must preserve enough failed background state to trigger fallback deterministically before generic turn teardown clears it
- terminal outcome ownership should remain turn-scoped; a failure cannot be allowed to outlive the active turn ambiguously

## Existing Blocking Compaction Paths To Reuse

### Manual blocking compaction

Manual compaction already has the exact blocking behavior Phase 4 wants as a recovery target.

The path is:

- `handlers::compact(...)` in `codex-rs/core/src/codex.rs`
- `CompactTask` in `codex-rs/core/src/tasks/compact.rs`
- local execution through `crate::compact::run_compact_task(...)`
- remote execution through `crate::compact_remote::run_remote_compact_task(...)`

Important contract details already covered by tests:

- manual compaction emits `ContextCompaction` started/completed items
- manual compaction keeps its warning and prompt behavior
- app-server `thread/compact/start` is a thin wrapper around `Op::Compact`

Planning implication:

- failed background auto-compaction should route into this existing `Op::Compact` / `CompactTask` flow instead of duplicating the summarization logic
- the fallback trigger likely belongs in the active turn orchestration layer, not inside local/remote compaction helpers

### Pre-turn protective compaction

Pre-turn protective compaction remains inline and blocking in `run_pre_sampling_compact(...)` / `run_auto_compact(...)`.

Existing tests in:

- `codex-rs/core/tests/suite/compact.rs`
- `codex-rs/core/tests/suite/compact_remote.rs`

already prove that when pre-turn compaction fails due to context-window pressure:

- the turn stops
- the follow-up model request does not continue
- the failure is surfaced to the user

Planning implication:

- Phase 4 should not rewire pre-turn protection onto the background-fallback state machine
- the safest plan is to add regression coverage proving pre-turn behavior remains exactly as-is while background recovery changes

## Existing State-Machine Constraints

`codex-rs/core/src/state/turn.rs` already gives Phase 4 a good skeleton:

- only one background auto-compaction may be in flight or awaiting apply at a time
- `BackgroundAutoCompactionOutcome` already distinguishes `Succeeded(...)` from `Failed(String)`
- `take_successful_completed_background_auto_compaction()` intentionally filters failed outcomes out of the success apply path

This means Phase 4 does not need a brand-new state model. It needs to tighten the transition rules so that:

- `Succeeded` still applies through the current Phase 2 path
- `Failed` transitions exactly once into fallback recovery
- cancellation or task teardown remains the only path to `aborted`

The main design risk is double-terminal behavior:

- a failed background compaction must not both trigger fallback and later get treated as a stale completed result
- cancellation during interruption must not also trigger fallback
- fallback failure should still surface through the blocking compaction path without leaving stale background markers behind

## Surface And API Implications

### Core event surfaces

Phase 4 will likely affect:

- `EventMsg::Error(...)`
- `EventMsg::TurnAborted(...)`
- `ItemStarted/ItemCompleted(ContextCompaction)` ordering around fallback

The user requirement is explicit: successful background compactions should stay non-interrupting, but failed ones should stop the agent and recover through the blocking path. That means interruption semantics should appear only on failure.

### App-server surfaces

App-server already exposes:

- `thread/compact/start` for manual blocking compaction
- `item/started` and `item/completed` notifications for `ContextCompaction`
- `thread/read`, `thread/resume`, and rollback surfaces that now preserve prior compaction turns

Phase 4 planning should assume app-server regression work is needed in:

- `codex-rs/app-server/tests/suite/v2/compaction.rs`
- possibly `codex-rs/app-server/tests/suite/v2/thread_rollback.rs` only if fallback recovery changes persisted history visibility

The key contract to preserve:

- manual compaction remains the explicit blocking RPC path
- successful auto-compaction still looks like background activity
- failure-driven fallback should not accidentally make manual and auto compaction indistinguishable on the wire unless that behavior is deliberate and tested

## Existing Test Coverage To Build On

### Core

Strong existing coverage already exists for:

- manual compaction items and blocking completion in `codex-rs/core/tests/suite/compact.rs`
- mid-turn background auto-compaction non-interruption in `codex-rs/core/tests/suite/compact.rs`
- pre-turn local and remote compaction failure behavior in `codex-rs/core/tests/suite/compact.rs` and `codex-rs/core/tests/suite/compact_remote.rs`
- interruption / `TurnAborted` behavior in `codex-rs/core/src/codex_tests.rs`

The missing Phase 4 coverage is:

- background worker failure interrupts the active turn
- background worker failure enters exactly one fallback recovery path
- fallback runs through the existing blocking compact task
- successful background compaction still does not interrupt
- cancelled background workers resolve as aborted, not failed-then-fallback

### App-server

Current app-server coverage already proves:

- auto-compaction started/completed notifications
- manual `thread/compact/start` behavior
- durable compaction marker visibility in `thread/read`, `thread/resume`, and rollback

The missing Phase 4 coverage is:

- failure-driven fallback behavior for auto-compaction without regressing manual blocking semantics
- notification ordering and thread-state behavior when a background compaction fails mid-turn

## Main Risks

### Silent failure risk

Failed background compactions are currently stored but ignored by the apply path. If Phase 4 only adds error surfacing without fallback, the system will violate `RECV-01`.

### Double-terminal risk

If failure handling races with generic turn teardown, the same background compaction could look both failed and aborted, or fallback could run twice.

### Guardrail regression risk

If fallback is wired by changing `run_auto_compact(...)` or `CompactTask` too broadly, manual compaction or pre-turn protection could accidentally become non-blocking or inherit the wrong interruption semantics.

### Surface drift risk

If app-server tests are not updated, recovery changes could regress notification sequencing or make failed background compaction look like a normal manual compaction without an explicit contract.

## Recommended Plan Shape

The cleanest Phase 4 breakdown is:

1. add core failure-state orchestration so failed background auto-compaction interrupts the active turn and hands control to the existing blocking compact path exactly once
2. preserve manual and pre-turn blocking guardrails explicitly, including no-regression coverage for the existing manual RPC path and pre-turn failure semantics
3. add core and app-server regressions that lock terminal outcomes and failure/fallback behavior without pulling in Phase 5 indicator work

This keeps the phase aligned with the roadmap:

- recovery semantics first
- guardrail preservation second
- regression proof last

## Likely Files To Change

- `codex-rs/core/src/codex.rs`
- `codex-rs/core/src/state/turn.rs`
- `codex-rs/core/src/tasks/mod.rs`
- `codex-rs/core/tests/suite/compact.rs`
- `codex-rs/core/tests/suite/compact_remote.rs`
- `codex-rs/core/src/codex_tests.rs`
- `codex-rs/app-server/tests/suite/v2/compaction.rs`

## Validation Architecture

### 1. Core recovery orchestration

Add targeted core tests proving that:

- failed background auto-compaction interrupts the active turn
- recovery uses the existing blocking compaction task
- successful background auto-compaction still remains non-interrupting
- cancellation resolves to aborted without fallback

The likely homes are:

- `codex-rs/core/tests/suite/compact.rs`
- `codex-rs/core/tests/suite/compact_remote.rs`
- `codex-rs/core/src/codex_tests.rs`

### 2. Guardrail preservation

Keep or extend explicit regressions for:

- manual `Op::Compact` / `thread/compact/start` remaining blocking
- pre-turn local compaction failure behavior
- pre-turn remote compaction failure behavior

These do not require new semantics; they need no-regression coverage while the background failure path changes.

### 3. App-server compatibility

Add app-server regression coverage only where Phase 4 changes observable behavior:

- failed background compaction notification / interruption sequencing
- manual compaction RPC remaining blocking

Do not pull in below-input status or multi-compaction scheduling; those belong to Phase 5.

## Open Questions

1. Should fallback be triggered immediately when the worker records `Failed(String)`, or only when the main turn loop next checks background completion state?
   Recommendation: plan around a turn-owned transition point in the main orchestration path so interruption, fallback, and cleanup stay serialized.

2. Should the failed background compaction marker remain visible in thread history once fallback succeeds?
   Recommendation: decide this only if an existing surface depends on it; otherwise keep Phase 4 focused on correctness and let Phase 5 own any user-visible status refinements.

---
*Phase: 04-failure-recovery-and-blocking-guardrails*
*Research completed: 2026-03-09*
