---
phase: 03-durable-history-and-surface-compatibility
plan: 03
subsystem: api
tags: [rust, app-server, protocol, rollback, testing]
requires:
  - phase: 03-01
    provides: proven core replay durability for persisted spliced compaction history
  - phase: 03-02
    provides: explicit app-server marker-only compaction history contract
provides:
  - end-to-end read and resume coverage for durable compaction markers
  - rollback compatibility coverage for prior compaction turns
  - protocol regression coverage for rollback-preserved compaction history
affects: [phase-3, thread-read, thread-resume, thread-rollback]
tech-stack:
  added: []
  patterns: [durable compaction compatibility is locked by cross-surface regressions]
key-files:
  created: []
  modified:
    - codex-rs/app-server-protocol/src/protocol/thread_history.rs
    - codex-rs/app-server/tests/suite/v2/compaction.rs
    - codex-rs/app-server/tests/suite/v2/thread_rollback.rs
key-decisions:
  - "Keep Phase 3 focused on compatibility proofs: add app-server and protocol regressions instead of widening runtime behavior changes."
  - "Poll thread/read for durable compaction markers in rollback coverage so the test observes persisted state rather than racing notifications."
patterns-established:
  - "Durability closeout should prove read, resume, and rollback surfaces all preserve prior context-compaction markers before moving to recovery or UX work."
requirements-completed: [HIST-03, COMP-03]
duration: 20min
completed: 2026-03-09
---

# Phase 3: Durable History And Surface Compatibility Summary

**Phase 3 now has direct protocol and app-server proof that durable compaction markers survive read, resume, and rollback flows without changing the existing compaction surface contract.**

## Performance

- **Duration:** 20 min
- **Started:** 2026-03-09T01:34:00-0500
- **Completed:** 2026-03-09T01:54:02-0500
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

- Added a protocol regression proving `thread_rollback` keeps the prior compaction turn visible after later turns are removed.
- Added an end-to-end app-server compaction test that verifies durable `ContextCompaction` items are still present in both `thread/read(includeTurns)` and `thread/resume` after automatic background compaction completes.
- Added an end-to-end rollback test that verifies a prior compaction turn remains visible after rolling back a later turn and after resuming the thread.

## Task Commits

1. **Task 1-2: Durable app-server compatibility regressions** - `7fac87600` (`test(app-server): cover durable compaction compatibility`)

## Files Created/Modified

- `codex-rs/app-server-protocol/src/protocol/thread_history.rs` - Added rollback coverage for preserving earlier compaction turns in built thread history.
- `codex-rs/app-server/tests/suite/v2/compaction.rs` - Added end-to-end read and resume coverage for durable background compaction markers.
- `codex-rs/app-server/tests/suite/v2/thread_rollback.rs` - Added rollback/resume coverage for preserving prior compaction turns and helper polling for durable thread history state.

## Decisions Made

- Used cross-surface regressions instead of reducer changes because the existing app-server behavior already matched the Phase 3 contract established in Wave 2.
- Waited for durable thread history state via `thread/read(includeTurns)` in rollback coverage so the test proves persisted compatibility instead of depending on notification timing.

## Deviations from Plan

None - Wave 3 delivered the planned regression coverage without reopening core replay logic.

## Issues Encountered

- The first rollback test revision raced against turn completion and initially missed the right turn lifecycle. The fix was to capture `TurnStartResponse` IDs, wait for matching `turn/completed` notifications, and poll `thread/read` until the compaction marker was durably visible.
- Scoped clippy also flagged an `expect()` in the new helper; it was replaced with `anyhow::Context` to keep the test failure path explicit without panicking.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

Phase 4 can now focus on failure recovery and blocking fallback behavior with durable compatibility locked across replay, read, resume, and rollback surfaces.

---
*Phase: 03-durable-history-and-surface-compatibility*
*Completed: 2026-03-09*
