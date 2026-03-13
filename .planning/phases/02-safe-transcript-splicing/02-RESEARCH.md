# Phase 2 Research: Safe Transcript Splicing

## What Phase 2 Needs To Deliver

Phase 2 is the first point where completed background compactions should mutate live transcript state. The phase goal is narrower than durability or fallback:

- when a background compaction finishes, only the transcript slice captured at compaction start is replaced
- any messages added after compaction started remain below the new compacted top section in the same order
- the active thread never shows duplicated, reordered, or dropped messages after a background result is applied

To stay aligned with `HIST-01` and `HIST-02`, Phase 2 should avoid broadening into later-phase work:

- do not add fallback-on-failure behavior yet; that is Phase 4
- do not introduce the visible below-input indicator yet; that is Phase 5
- do not fully solve replay/read/resume/app-server parity beyond what naturally follows from the existing live apply path; that is Phase 3

## Current Implementation Seams

Phase 1 already established the runtime shell needed for this phase:

- `codex-rs/core/src/codex.rs` `start_background_auto_compact(...)` snapshots history with `sess.clone_history().await`, launches the worker, and stores the completed result on active-turn state
- `codex-rs/core/src/state/turn.rs` now has one in-flight background job slot and one retained completed outcome slot
- `codex-rs/core/src/tasks/mod.rs` cancels and clears the background state when the active turn ends

The result objects already contain the data needed to replace a full history snapshot:

- `codex-rs/core/src/compact.rs` `LocalCompactResult`
- `codex-rs/core/src/compact_remote.rs` `RemoteCompactionResult`

Both result types already carry:

- `replacement_history`
- `reference_context_item`
- `compacted_item`

The missing Phase 2 piece is safe application against a live history that has changed since the snapshot was taken.

## Relevant History APIs

The live session history is owned by `ContextManager` in `codex-rs/core/src/context_manager/history.rs`.

Important operations:

- `raw_items()` exposes the current ordered history
- `replace(...)` replaces the entire in-memory history
- `record_items(...)` only appends normalized API-visible items
- `remove_first_item(...)` / `remove_last_item(...)` preserve call/output pairing invariants

Session-level apply hooks live in `codex-rs/core/src/codex.rs`:

- `replace_history(...)`
- `replace_compacted_history(...)`
- `recompute_token_usage(...)`

`replace_compacted_history(...)` already does the session mutations Phase 2 wants once the correct replacement history is known:

1. replace in-memory history
2. persist the compacted rollout item
3. persist `TurnContextItem` when `reference_context_item` is present

That means Phase 2 should not invent a separate transcript-apply path. It should compute the correct replacement history for the current live state, then reuse the existing apply primitive.

## Key Planning Implication: The Splice Must Be Prefix-Based

Phase 1 background compaction snapshots the entire history at compaction start. While the worker runs, the active turn may append newer items. In the normal case, the live history during apply should therefore have this shape:

- `prefix`: the exact history snapshot seen by the background worker
- `tail`: newer items appended after compaction started

The desired Phase 2 splice is:

- `new_history = replacement_history + tail`

This is intentionally not an arbitrary range replace. The current runtime only starts background compaction from a full cloned history snapshot, so the safe Phase 2 contract can remain:

- replace the exact snapshot prefix
- preserve everything appended after that prefix

This is enough to satisfy `HIST-01` and `HIST-02` without taking on Phase 5’s multi-compaction overlap complexity.

## What Metadata Phase 2 Still Needs

The stored background result currently knows the replacement history, but not enough about the original snapshot boundary to splice safely. Planning should assume the completed background outcome must retain at least one of:

- the full captured snapshot history
- an exact prefix fingerprint plus snapshot length

The simplest and most auditable Phase 2 shape is to retain the exact captured snapshot history for a single in-flight background job. This keeps the splice check straightforward:

- if the current live history begins with the captured snapshot, apply `replacement_history + tail`
- if it does not, do not force-apply the result

Because Phase 1 is single-flight per active turn, this memory cost is acceptable for Phase 2.

## Recommended Apply Point

Background result application should stay on the main turn runtime path, not the detached worker task.

Why:

- the main turn already owns active-turn semantics
- it avoids background-worker mutation racing with live sampling code
- it keeps all transcript mutation on the same orchestration path that already owns token recomputation and history persistence

Practical shape:

- worker stores a completed background outcome with snapshot metadata
- `run_turn()` or a nearby turn-owned loop polls for a completed outcome at safe points
- when a completed result exists, the main turn applies the splice once, clears the stored outcome, and continues

## Scope Boundaries To Preserve

Phase 2 should stay strict about what it is *not* solving:

- no fallback on failed background compaction
- no concurrent application of multiple background jobs
- no separate user-facing status indicator changes
- no special replay-only or app-server-only compatibility work beyond what comes from using `replace_compacted_history(...)`

The live transcript splice can land first as long as it reuses the existing persistence path instead of creating a temporary live-only fork.

## Likely Files To Change

- `codex-rs/core/src/state/turn.rs`
  - extend stored background result state with snapshot metadata
  - add take/peek helpers for completed background outcomes
- `codex-rs/core/src/codex.rs`
  - add a turn-owned apply path for completed background compactions
  - use a prefix splice guard before mutating live history
- `codex-rs/core/src/context_manager/history.rs`
  - add a pure helper that replaces a captured prefix while preserving appended tail
- `codex-rs/core/src/compact.rs`
  - if needed, expose or thread snapshot-related metadata into the stored local result
- `codex-rs/core/src/compact_remote.rs`
  - same, for remote results
- `codex-rs/core/src/codex_tests.rs`
  - add direct unit/integration coverage for the splice helper and turn-owned application
- `codex-rs/core/tests/suite/compact.rs`
  - assert local background compaction actually rewrites live history after completion
- `codex-rs/core/tests/suite/compact_remote.rs`
  - assert remote background compaction rewrites live history after completion

## Main Risks

### Prefix mismatch risk

If the live history no longer begins with the captured snapshot when the result completes, Phase 2 must not blindly apply the replacement history. Forcing an apply would drop or reorder newer items.

### Reference-context risk

`replacement_history` may carry a `reference_context_item`, and `replace_compacted_history(...)` persists it. The splice helper must preserve the existing reference-context semantics when replacing the prefix, or later turns may diff against the wrong baseline.

### Call/output pairing risk

Any manual splice helper added below the `Session` layer must preserve the history invariants that `ContextManager` relies on. Rebuilding the full history vector from `replacement_history + tail` is safer than trying to surgically delete items in place.

### Timing risk

Applying a completed result from the worker task itself risks racing the active turn while new items are still being recorded. Keep transcript mutation on the turn-owned path.

## Recommended Plan Shape

The cleanest Phase 2 breakdown is:

1. add snapshot metadata and a pure prefix-splice helper
2. wire completed background results into the main turn runtime and clear them after apply
3. add regression coverage for local and remote continuation order, no-duplication, and preserved newer messages

That keeps the phase coherent and makes it easy for Phase 3 to focus on persistence/replay parity rather than live transcript correctness.

## Validation Architecture

### 1. Pure splice correctness

Add direct unit coverage for the prefix-splice helper using synthetic histories:

- exact prefix replacement with no tail
- exact prefix replacement with appended tail
- tail preserved in order after splice
- mismatch guard rejects or skips apply when the prefix has diverged

The best home is a focused helper test near `ContextManager` or `codex_tests.rs`.

### 2. Live local and remote application

Update `codex-rs/core/tests/suite/compact.rs` and `codex-rs/core/tests/suite/compact_remote.rs` so the successful background path no longer stops at “result stored”. The tests should assert:

- after compaction completes, the live history seen by the continuation request contains the compacted prefix
- newer messages created while compaction was running still appear below that prefix
- no duplicated tool/output or summary items appear

### 3. Turn-owned lifecycle safety

Keep the existing Phase 1 invariants:

- background compaction still does not replace the active turn
- only one background compaction is active per turn
- completion is applied exactly once and then cleared from active-turn state

## Open Questions

1. Should the stored completed background outcome retain the full captured snapshot or a smaller fingerprint?
   Recommendation: store the full snapshot in Phase 2 for clarity and deterministic testing.

2. Where should the pure splice helper live?
   Recommendation: place it close to `ContextManager` or `Session` history mutation code so it can be reused by later phases without duplicating transcript logic.

3. Should Phase 2 add any replay assertions immediately?
   Recommendation: only the minimal assertions that naturally follow from reusing `replace_compacted_history(...)`. Broader replay/read/resume parity remains Phase 3.
