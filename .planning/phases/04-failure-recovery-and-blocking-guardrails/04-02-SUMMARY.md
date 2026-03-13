---
phase: 04-failure-recovery-and-blocking-guardrails
plan: 02
subsystem: core
tags: [rust, core, compaction, recovery, testing]
requires:
  - phase: 04-failure-recovery-and-blocking-guardrails
    provides: explicit failed background compaction consume path
provides:
  - failure-only interruption for background auto-compaction
  - blocking fallback reuse for local and remote auto-compaction failures
  - deterministic end-of-turn recovery coverage for failed background compactions
affects: [phase-4, background-compaction, recovery]
tech-stack:
  added: []
  patterns: [turn-owned background completion signaling, matched response sequencing for duplicate compaction requests]
key-files:
  created:
    - .planning/phases/04-failure-recovery-and-blocking-guardrails/04-02-SUMMARY.md
  modified:
    - codex-rs/core/src/codex.rs
    - codex-rs/core/src/state/turn.rs
    - codex-rs/core/src/codex_tests.rs
    - codex-rs/core/tests/common/responses.rs
    - codex-rs/core/tests/suite/compact.rs
    - codex-rs/core/tests/suite/compact_remote.rs
key-decisions:
  - "Recover failed background compactions from the regular turn loop by reusing `run_auto_compact(...)` instead of adding a second fallback implementation."
  - "Add a turn-owned background completion signal so end-of-turn recovery can briefly wait for just-finished failures without turning successful remote background compaction into a new blocking path."
  - "Use a longer end-of-turn grace period for local compaction than remote compaction because the local path needs more time to surface failed outcomes reliably while remote timing-sensitive tests only need a short grace window."
patterns-established:
  - "Failed background auto-compaction is now recovered through the same blocking compaction entrypoint used by existing inline flows."
  - "Tests that exercise repeated compaction requests with identical shapes should use matched response sequences instead of competing one-shot mocks."
requirements-completed: [RECV-01, RECV-02]
duration: 38min
completed: 2026-03-09
---

# Phase 4: Failure Recovery And Blocking Guardrails Summary

**Wave 2 made failed background auto-compaction interrupt only on failure, then recover through the existing blocking compaction path for both local and remote flows.**

## Performance

- **Duration:** 38 min
- **Started:** 2026-03-09T09:30:43-0500
- **Completed:** 2026-03-09T10:08:08-0500
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments

- Taught the regular turn loop to consume failed completed background compactions, rerun blocking compaction once, and stop the active turn without emitting a separate abort event.
- Added a turn-owned background completion signal so end-of-turn recovery can catch just-finished failures deterministically instead of relying on scheduler timing.
- Locked local and remote regressions around the failure path, including a local matched-response sequence for identical compaction request shapes and a remote path that still preserves non-interrupting success behavior.
- Tightened a remote history test so it waits for setup turns to finish instead of assuming an exact number of pre-compaction model requests.

## Task Commits

1. **Task 1-2: Failure-only interruption and blocking fallback reuse** - `9f6c2f448` (`feat(core): recover failed background compaction`)

## Files Created/Modified

- `codex-rs/core/src/codex.rs` - Added failed background recovery in the regular turn loop plus backend-specific end-of-turn grace handling.
- `codex-rs/core/src/state/turn.rs` - Added background completion signaling for active-turn background compaction state.
- `codex-rs/core/src/codex_tests.rs` - Updated focused core tests that construct background compaction state directly.
- `codex-rs/core/tests/common/responses.rs` - Added matched response-sequence support for repeated `/responses` requests with the same matcher.
- `codex-rs/core/tests/suite/compact.rs` - Added stable local fallback coverage for failed background auto-compaction.
- `codex-rs/core/tests/suite/compact_remote.rs` - Added and stabilized remote fallback coverage plus remote setup-turn timing assertions.

## Decisions Made

- Kept failure recovery in `run_turn(...)` so interruption, fallback, and turn termination all stay serialized under the active turn instead of splitting behavior across teardown or worker code.
- Used background completion signaling rather than a pure `yield_now()` retry loop because the scheduler-based approach was too flaky under the full compaction suite.
- Made the remote setup-history test assert on completed setup turns instead of exact request counts because request interleaving is not part of the behavior under test.

## Deviations from Plan

Minor: Wave 2 needed one extra test-helper change in `codex-rs/core/tests/common/responses.rs` so local fallback coverage could drive two identical compaction requests through ordered responses without brittle matcher races.

## Issues Encountered

- A simple scheduler-yield approach made failure recovery flaky in both local and remote suites.
- A broad end-of-turn wait stabilized failure recovery but changed remote timing-sensitive tests; narrowing the remote grace window and tightening the tests around actual behavior resolved that without weakening failure coverage.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

Wave 3 can now lock manual and pre-turn blocking guardrails directly, with failed background recovery already proven through the shared blocking compaction path.

---
*Phase: 04-failure-recovery-and-blocking-guardrails*
*Completed: 2026-03-09*
