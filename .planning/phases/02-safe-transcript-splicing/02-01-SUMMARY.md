---
phase: 02-safe-transcript-splicing
plan: 01
subsystem: history
tags: [compaction, history, snapshot, splice]
requires: []
provides:
  - Completed background auto-compaction outcomes retain the captured snapshot history needed for safe prefix replacement
  - ContextManager exposes a pure prefix-splice helper that preserves appended tail items in order
  - Prefix mismatch is detectable before any live-history mutation occurs
affects: [02-02, 02-03, phase-3-durable-history-and-surface-compatibility]
tech-stack:
  added: []
  patterns: [captured snapshot metadata, prefix splice helper]
key-files:
  created: []
  modified:
    - codex-rs/core/src/state/turn.rs
    - codex-rs/core/src/context_manager/history.rs
key-decisions:
  - "Stored the full captured snapshot history for the single in-flight background job instead of a smaller fingerprint so Phase 2 splice checks stay deterministic"
  - "Kept the splice primitive pure and prefix-based so live history is rebuilt as replacement history plus preserved tail instead of mutated in place"
patterns-established:
  - "Background compaction application is guarded by exact-prefix matching against the captured history snapshot"
requirements-completed: [HIST-01]
duration: 1h 00m
completed: 2026-03-09
---

# Phase 2 Plan 01: Snapshot Metadata And Prefix Splice Summary

**Phase 2 now has the snapshot metadata and pure splice primitive needed to replace only the captured transcript prefix**

## Performance

- **Duration:** 1h 00m
- **Completed:** 2026-03-09
- **Files modified:** 2

## Accomplishments

- Extended active-turn background compaction state in `codex-rs/core/src/state/turn.rs` so completed outcomes keep the captured snapshot history alongside the result payload
- Added `ContextManager::splice_compacted_prefix(...)` in `codex-rs/core/src/context_manager/history.rs` to rebuild live history as `replacement_history + tail` only when the current history still begins with the captured snapshot
- Added focused unit coverage proving the helper preserves appended tail ordering and rejects diverged prefixes before any live mutation occurs

## Task Commits

1. **Task 1: Extend stored background outcomes with snapshot splice metadata** - `b36c7358e` (`feat(core): splice completed background compactions`)
2. **Task 2: Add a pure prefix-splice helper for compaction application** - `b36c7358e` (`feat(core): splice completed background compactions`)

## Files Created/Modified

- `codex-rs/core/src/state/turn.rs` - stores captured snapshot history with completed background compaction outcomes
- `codex-rs/core/src/context_manager/history.rs` - adds the pure prefix-splice helper used by the live apply path

## Decisions Made

- Retained the full captured snapshot for the one in-flight background compaction slot because it keeps splice verification simple and auditable
- Rebuilt spliced history from whole vectors instead of trying to delete or replace items in place, which preserves existing history invariants cleanly

## Deviations from Plan

None.

## Verification

- `cargo test -p codex-core compact`

## Next Phase Readiness

- Plan 02 can apply completed background results through the existing `replace_compacted_history(...)` path using the new snapshot guard
- Plan 03 can lock the splice contract with helper-level and integration coverage

---
*Phase: 02-safe-transcript-splicing*
*Completed: 2026-03-09*
