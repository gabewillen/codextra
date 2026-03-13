# Phase 1 Research: Background Trigger And Continued Turns

## What Phase 1 Needs To Deliver

Phase 1 is about runtime behavior, not final transcript correctness. The codebase already has automatic compaction, but it runs inline on the active turn. The planning target should be:

- automatic mid-turn compaction starts without a user action
- the active turn keeps running while compaction is in flight
- agent progress continues to stream during that time

To keep scope aligned with `RUN-01` and `RUN-02`, Phase 1 should avoid pulling in later-phase concerns:

- do not redesign manual `/compact` in `codex-rs/core/src/tasks/compact.rs`
- do not change pre-turn protective compaction semantics in `codex-rs/core/src/codex.rs`
- do not solve safe transcript splice/application yet; that belongs to Phase 2
- do not solve fallback-on-failure yet; that belongs to Phase 4

## Current Implementation Seams

The current trigger path is in `codex-rs/core/src/codex.rs`.

- `run_turn()` triggers mid-turn auto compaction after a sampling response when `token_limit_reached && needs_follow_up`.
- `run_pre_sampling_compact()` handles the blocking pre-turn path.
- `maybe_run_previous_model_inline_compact()` handles the blocking smaller-context-window model-switch path.
- `run_auto_compact()` dispatches to `codex-rs/core/src/compact.rs` or `codex-rs/core/src/compact_remote.rs`.

Today this is synchronous. `run_turn()` calls `run_auto_compact()` inline and waits for it before the turn loop continues. That means Phase 1 cannot be implemented by tweaking thresholds only; it needs a new execution shape.

The other important seam is task ownership:

- `codex-rs/core/src/tasks/mod.rs` `spawn_task()` always aborts the existing turn and installs a fresh `ActiveTurn`.
- `codex-rs/core/src/state/turn.rs` `ActiveTurn` tracks one logical active turn plus its `TurnState`.

That means Phase 1 should not reuse `spawn_task()` for background compaction. Doing so would replace the active agent turn, which is the opposite of the requirement.

There is already a useful local pattern for auxiliary work: `handlers::run_user_shell_command()` in `codex-rs/core/src/codex.rs` launches an auxiliary operation against the active turn without replacing it, and `codex-rs/core/tests/suite/user_shell_cmd.rs` verifies that behavior. Phase 1 should copy that ownership pattern, not the standalone task path.

## Recommended Implementation Shape

### 1. Split compaction into worker and commit phases

`codex-rs/core/src/compact.rs` and `codex-rs/core/src/compact_remote.rs` currently do two jobs at once:

- build and execute the compaction request
- immediately mutate live session history via `replace_compacted_history()`

Phase 1 should separate those responsibilities. The worker side should accept a frozen history snapshot and return a result object. The commit/apply side should remain session-owned.

Practical implication: introduce a result type that contains the data later phases will need, for example replacement history plus compaction metadata, but do not require Phase 1 to apply it mid-turn.

### 2. Start background compaction from `run_turn()`, but keep the turn loop moving

The mid-turn trigger still belongs in `codex-rs/core/src/codex.rs` `run_turn()`. What changes is what happens after trigger:

- capture a history snapshot from `sess.clone_history().await`
- launch a detached auxiliary async job with a child cancellation token
- continue the regular turn loop immediately instead of awaiting compaction

This should stay single-job for Phase 1. If one background auto-compaction is already running for the active turn, suppress additional auto triggers until that job finishes or is cancelled. Multi-job overlap is explicitly Phase 5 work.

### 3. Use separate model-client state for the background worker

This matters for local compaction. The active turn reuses one `ModelClientSession` across follow-up requests. Background compaction should not share that session.

Why:

- local compaction in `codex-rs/core/src/compact.rs` already creates a fresh client session internally
- `codex-rs/core/tests/suite/turn_state.rs` shows turn-state headers persist within a turn
- sharing client-session state between the active turn and background compaction would risk cross-talk in websocket/request state

Recommendation: keep background compaction on its own model-client session or endpoint call, even though it belongs to the same logical turn.

### 4. Track background compaction explicitly on active-turn state

Phase 1 likely needs a small amount of new state in `codex-rs/core/src/state/turn.rs` and possibly `codex-rs/core/src/state/session.rs`:

- whether an auto background compaction is in flight
- the snapshot identity or generation it was created from
- join handle / cancellation handle
- terminal result storage if the worker finishes before later phases can safely apply it

There is no obvious existing history generation counter in the current context manager. Planning should assume Phase 1 may need to add one, even if the first use is only duplicate suppression and stale-result bookkeeping.

## Scope-Safe Recommendation For Phase 1

To preserve the roadmap boundary, the safest Phase 1 plan is:

1. Trigger and run background compaction from a frozen snapshot.
2. Keep the active turn alive and streaming.
3. Track the finished result, but do not splice it into live history if newer turn output has appeared.

That keeps `RUN-01` and `RUN-02` achievable without silently dragging `HIST-01` and `HIST-02` into the same phase.

If planning insists on applying a result in Phase 1, it should only do so under a strict no-new-history-written condition. Anything more complicated becomes Phase 2 transcript-splice work.

## Likely Files To Change

- `codex-rs/core/src/codex.rs`
  - change the mid-turn trigger path in `run_turn()`
  - add auxiliary background-job launch/cancel logic
  - gate duplicate auto-compaction starts while one job is active
- `codex-rs/core/src/state/turn.rs`
  - add active-turn background-compaction tracking
- `codex-rs/core/src/state/session.rs`
  - add session-side bookkeeping only if result storage or history generation lives there
- `codex-rs/core/src/compact.rs`
  - refactor local compaction so request execution can return a snapshot-derived result without mutating live history
- `codex-rs/core/src/compact_remote.rs`
  - same refactor for remote compaction
- `codex-rs/core/src/tasks/mod.rs`
  - probably minimal changes only if shared abort/metrics helpers are extracted; do not route background compaction through `spawn_task()`
- `codex-rs/core/tests/suite/compact.rs`
  - update local auto-compaction behavior tests
- `codex-rs/core/tests/suite/compact_remote.rs`
  - update remote auto-compaction behavior tests
- `codex-rs/app-server/tests/suite/v2/compaction.rs`
  - update if compaction item timing or event ordering changes

## Main Risks

### Phase-boundary risk

The largest risk is implementing transcript application too early. Both `codex-rs/core/src/compact.rs` and `codex-rs/core/src/compact_remote.rs` currently end by replacing live history. If that behavior remains attached to the background worker, newer turn output can be overwritten.

### Active-turn ownership risk

`spawn_task()` in `codex-rs/core/src/tasks/mod.rs` replaces the active turn. Reusing it for background compaction would violate the phase goal immediately.

### Cancellation risk

The background worker must be cancelled when the active turn is interrupted or replaced. The auxiliary path should inherit a child `CancellationToken` from the running turn, similar to the active-turn auxiliary shell-command flow.

### Event semantics risk

`BackgroundEvent` in `codex-rs/protocol/src/protocol.rs` is not persisted by `codex-rs/core/src/rollout/policy.rs`. That is acceptable for Phase 1 runtime/debug visibility, but not for durable UX. Do not depend on it for replay/resume semantics in this phase.

## Test Approach

Primary coverage should stay in `codex-rs/core/tests/suite/compact.rs` and `codex-rs/core/tests/suite/compact_remote.rs`.

The most valuable new tests are:

- delayed background compaction starts automatically after threshold crossing, without any extra user input
- while the compaction request is still blocked, the active turn continues and emits more model progress
- no `TurnAborted(Replaced)` or extra `TurnStarted`/`TurnComplete` lifecycle is emitted for auto-compaction
- only one background auto-compaction runs per active turn in Phase 1
- interrupting the active turn cancels the background compaction

Reference pattern:

- `codex-rs/core/tests/suite/user_shell_cmd.rs` already proves an auxiliary operation can run during an active turn without replacing it

Less important for Phase 1:

- replay/resume durability tests
- final history splice correctness tests
- fallback-on-failure tests

Those belong to later phases unless Phase 1 deliberately expands scope.

## Validation Architecture

Validation should be split into three layers.

### 1. Core concurrency behavior

Use integration tests in `codex-rs/core/tests/suite/compact.rs` and `codex-rs/core/tests/suite/compact_remote.rs` with delayed mock responses so compaction is provably still in flight while the main turn continues.

### 2. Active-turn invariants

Assert:

- the active turn remains the same turn id
- auto-compaction does not create a replacement task lifecycle
- steering/pending-input semantics still work while the turn is active

Existing `steer_input` coverage in `codex-rs/core/src/codex_tests.rs` is a good place for small targeted state tests if new `ActiveTurn` bookkeeping is added.

### 3. Event-surface compatibility

If Phase 1 keeps emitting compaction item lifecycle events, update `codex-rs/app-server/tests/suite/v2/compaction.rs` to assert the new ordering. If Phase 1 only emits background runtime messages, validate that no transcript-visible regression is introduced for the active turn.

## Open Questions

1. Should Phase 1 store completed background results and defer application entirely until Phase 2, or allow apply-only-when-no-new-history-was-written?
   Recommendation: defer or use a strict no-new-history guard.

2. Should successful background auto-compaction still emit `ContextCompaction` item started/completed events before Phase 5 changes the visible UX?
   Recommendation: decide this explicitly during planning, because it affects app-server tests and transcript/event expectations.

3. Where should the in-flight background state live?
   Recommendation: start in `codex-rs/core/src/state/turn.rs`, because the lifecycle is tied to the active turn, not the whole session.

4. What is Phase 1 failure behavior?
   Recommendation: do not pull Phase 4 fallback into this phase. Cancel or quarantine failed background results and keep failure-recovery semantics as a later plan item.
