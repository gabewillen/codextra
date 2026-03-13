---
phase: 04-failure-recovery-and-blocking-guardrails
plan: 01
subsystem: core
tags: [rust, core, compaction, state-machine, testing]
requires:
  - phase: 03-durable-history-and-surface-compatibility
    provides: durable completed background compaction apply path
provides:
  - explicit failed background compaction consume path
  - single-terminal-state coverage for failed outcomes
  - foundation for Phase 4 failure fallback orchestration
affects: [phase-4, background-compaction, recovery]
tech-stack:
  added: []
  patterns: [failed background compaction is a turn-owned consumable terminal state]
key-files:
  created: []
  modified:
    - codex-rs/core/src/state/turn.rs
    - codex-rs/core/src/codex_tests.rs
key-decisions:
  - "Keep Wave 1 narrow: expose failed completed background compactions explicitly in active-turn state without adding fallback behavior yet."
  - "Treat failed background outcomes as consumable exactly once so later fallback logic can be serialized in the regular turn loop."
patterns-established:
  - "Background auto-compaction terminal outcomes should be consumed through explicit state helpers instead of implicit teardown side effects."
requirements-completed: [RECV-02]
duration: 12min
completed: 2026-03-09
---

# Phase 4: Failure Recovery And Blocking Guardrails Summary

**Wave 1 turned failed background auto-compaction into an explicit turn-owned terminal state that core can consume exactly once before fallback logic is added.**

## Performance

- **Duration:** 12 min
- **Started:** 2026-03-09T09:18:00-0500
- **Completed:** 2026-03-09T09:30:43-0500
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- Added an explicit active-turn helper for consuming failed completed background auto-compactions without routing them through the existing success-only apply path.
- Added core regression coverage proving failed completed outcomes block new background runs until consumed, bypass the success consume helper, and are consumable exactly once.
- Confirmed the Wave 1 foundation did not require teardown changes in `tasks/mod.rs`; the critical missing behavior was explicit failed-outcome consumption, not different turn-finish cleanup semantics.

## Task Commits

1. **Task 1-2: Failed background terminal-state foundation** - `559cf3db6` (`test(core): support failed compaction terminal state`)

## Files Created/Modified

- `codex-rs/core/src/state/turn.rs` - Added a failed-outcome consume helper for completed background auto-compactions.
- `codex-rs/core/src/codex_tests.rs` - Added core coverage for single-consumption failed terminal outcomes and reopened background eligibility after consumption.

## Decisions Made

- Kept failed-outcome handling in `ActiveTurn` rather than introducing a new recovery container so Phase 4 fallback can stay serialized in the regular turn loop.
- Deferred any interruption or blocking fallback behavior to Wave 2 so Wave 1 could prove the single-terminal state contract independently.

## Deviations from Plan

Minor: no code change was needed in `codex-rs/core/src/tasks/mod.rs` after inspection. Existing teardown behavior already remains turn-end-only; the real missing seam was a first-class failed-outcome consume path in active-turn state.

## Issues Encountered

- The initial helper implementation produced a dead-code warning in normal library builds because Wave 1 uses it only from tests. The method was gated to test builds for now; Wave 2 can lift that once runtime fallback consumes the failed outcome directly.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

Wave 2 can now implement failure-only interruption and blocking fallback on top of an explicit failed-outcome state transition instead of relying on teardown timing.

---
*Phase: 04-failure-recovery-and-blocking-guardrails*
*Completed: 2026-03-09*
