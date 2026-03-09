---
phase: 05-visible-rolling-background-compaction
plan: 01
subsystem: core
tags: [rust, core, compaction, state-machine, testing]
requires:
  - phase: 04-failure-recovery-and-blocking-guardrails
    provides: single-shot background failure recovery contract
provides:
  - rolling background compaction job tracking
  - distinct-range launch gating
  - launch-ordered completed job foundation
affects: [phase-5, background-compaction, rolling-overlap]
tech-stack:
  added: []
  patterns:
    - rolling background compactions are tracked as ordered per-turn jobs instead of a single in-flight/completed slot
key-files:
  created: []
  modified:
    - codex-rs/core/src/state/turn.rs
    - codex-rs/core/src/codex.rs
    - codex-rs/core/src/tasks/mod.rs
    - codex-rs/core/src/codex_tests.rs
key-decisions:
  - "Gate new automatic background compactions by captured transcript length so only newer ranges can overlap."
  - "Keep completed background jobs ordered by launch ordinal so later waves can apply or recover them deterministically even if they finish out of order."
patterns-established:
  - "Rolling background compaction state should expose collection-based helpers for in-flight jobs, completed jobs, and failure notifies."
requirements-completed: [RUN-03]
duration: 36min
completed: 2026-03-09
---

# Phase 5: Visible Rolling Background Compaction Summary

**Wave 1 replaced the single background-compaction slot with rolling tracked jobs and distinct-range launch gating in core.**

## Performance

- **Duration:** 36 min
- **Started:** 2026-03-09T10:36:00-0500
- **Completed:** 2026-03-09T11:12:04-0500
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments

- Replaced `ActiveTurn`'s single in-flight/completed background compaction state with collections that can track multiple rolling jobs.
- Added launch metadata (`snapshot_history_len` and `launch_ordinal`) so the turn loop can reject duplicate/older snapshot launches and preserve deterministic completion order.
- Updated task teardown and failure-wait plumbing to work against multiple tracked background compactions instead of a single slot.
- Added focused core tests proving overlap is allowed only for newer captured ranges and that completed jobs remain launch-ordered even when they finish out of order.

## Task Commits

1. **Task 1-2: Rolling job tracking and launch gating** - pending phase commit

## Files Created/Modified

- `codex-rs/core/src/state/turn.rs` - Replaced single-slot background compaction state with rolling tracked jobs and ordered completed outcomes.
- `codex-rs/core/src/codex.rs` - Updated failure waiting and launch/cancellation plumbing to use the new multi-job state helpers.
- `codex-rs/core/src/tasks/mod.rs` - Cancelled and cleared all tracked background compactions during turn teardown.
- `codex-rs/core/src/codex_tests.rs` - Added rolling overlap state tests and updated existing background compaction tests to the new state model.

## Decisions Made

- Used captured history length as the first overlap gate because it is monotonic for the current append-only mid-turn flow and keeps the launch rule simple.
- Ordered completed jobs by launch rather than finish time so later apply/recovery logic has a deterministic base to build on.

## Deviations from Plan

Minor: `codex-rs/core/src/tasks/mod.rs` needed a small Wave 1 update so teardown could cancel multiple in-flight background jobs safely. That still fits the plan boundary because the state refactor would otherwise leave turn cleanup inconsistent.

## Issues Encountered

- Waiting on "any failure notify" across multiple background compactions initially hit a Tokio pinning issue with `select_all`; wrapping each wait in a boxed async future resolved it cleanly.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

Wave 2 can now build deterministic rolling apply/fallback behavior on top of ordered tracked jobs instead of inventing a second overlap registry.

---
*Phase: 05-visible-rolling-background-compaction*
*Completed: 2026-03-09*
