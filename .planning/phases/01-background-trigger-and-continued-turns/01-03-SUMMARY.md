---
phase: 01-background-trigger-and-continued-turns
plan: 03
subsystem: runtime
tags: [compaction, runtime, background, active-turn]
requires: [01-01, 01-02]
provides:
  - Mid-turn auto-compaction starts as auxiliary active-turn work instead of replacing the turn
  - Active-turn teardown cancels in-flight background compaction and clears stored outcomes
  - Completed Phase 1 background compaction results are retained without being applied to live history
affects: [01-04, phase-2-safe-transcript-splicing, phase-4-failure-recovery-and-blocking-guardrails]
tech-stack:
  added: []
  patterns: [turn-owned auxiliary worker state, deferred background result storage]
key-files:
  created: []
  modified:
    - codex-rs/core/src/codex.rs
    - codex-rs/core/src/state/mod.rs
    - codex-rs/core/src/state/turn.rs
    - codex-rs/core/src/tasks/mod.rs
key-decisions:
  - "Tracked background auto-compaction outside the main task map so it cannot replace the active turn through existing task semantics"
  - "Deferred all background result application to Phase 2 and limited Phase 1 to launch, lifecycle, and storage"
patterns-established:
  - "Active turns can own one auxiliary compaction worker plus one completed compaction outcome for later phases"
requirements-completed: [RUN-01, RUN-02]
duration: 1h 35m
completed: 2026-03-09
---

# Phase 1 Plan 03: Background Trigger And Continued Turns Summary

**Automatic mid-turn compaction now starts as background work while the active agent turn keeps running**

## Performance

- **Duration:** 1h 35m
- **Started:** 2026-03-09T04:25:00Z
- **Completed:** 2026-03-09T06:00:00Z
- **Tasks:** 3
- **Files modified:** 4

## Accomplishments

- Replaced the inline post-sampling auto-compaction path in `codex-rs/core/src/codex.rs` with `start_background_auto_compact(...)`, so the turn loop keeps sampling instead of `continue`-ing into a blocking compact call
- Added active-turn bookkeeping for one auxiliary background compaction worker and one completed background result in `codex-rs/core/src/state/turn.rs`
- Updated turn teardown in `codex-rs/core/src/tasks/mod.rs` to cancel and clear background compaction state when the active turn ends, is replaced, or is interrupted
- Kept Phase 1 result handling storage-only: successful background outcomes emit compaction item completion but do not mutate live history yet

## Task Commits

1. **Task 1: Add active-turn bookkeeping for one auxiliary auto-compaction job** - `851568545` (`feat(core): run mid-turn auto compaction in background`)
2. **Task 2: Launch background compaction from `run_turn()` without blocking the loop** - `851568545` (`feat(core): run mid-turn auto compaction in background`)
3. **Task 3: Defer all background result application to Phase 2** - `851568545` (`feat(core): run mid-turn auto compaction in background`)

## Files Created/Modified

- `codex-rs/core/src/codex.rs` - launches auxiliary background auto-compaction and keeps the active turn running
- `codex-rs/core/src/state/mod.rs` - re-exports background compaction turn-state types
- `codex-rs/core/src/state/turn.rs` - tracks in-flight and completed background compaction state for the active turn
- `codex-rs/core/src/tasks/mod.rs` - cancels and clears auxiliary compaction work during turn teardown

## Decisions Made

- Kept auxiliary background compaction off the normal task lifecycle so Phase 1 could preserve current task replacement behavior
- Stored completed local and remote compaction outcomes without applying them, leaving transcript splice correctness to Phase 2
- Boxed the stored success result variant to keep the new turn-state type free of clippy warnings during `just fix -p codex-core`

## Deviations from Plan

None.

## Issues Encountered

- The first implementation introduced a `large_enum_variant` clippy warning on the stored background outcome type. Boxing the success payload kept the state representation warning-free without changing runtime behavior.

## User Setup Required

None.

## Verification

- `cargo test -p codex-core compact`
- `cargo test -p codex-core user_shell_cmd`
- `cargo test -p codex-app-server compaction`
- `just fix -p codex-core`
- `just fmt`

## Next Phase Readiness

- Phase 2 can splice a completed background result into the captured transcript range without changing how background jobs are launched or cancelled
- Phase 4 can add failure fallback on top of the stored Phase 1 background outcome instead of redesigning the turn runtime

---
*Phase: 01-background-trigger-and-continued-turns*
*Completed: 2026-03-09*
