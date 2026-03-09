---
phase: 02-safe-transcript-splicing
plan: 03
subsystem: tests
tags: [compaction, tests, regression, ordering]
requires: [02-02]
provides:
  - Helper-level coverage for exact-prefix replacement, mismatch skipping, and single-apply clearing semantics
  - Local and remote regression coverage for immediate continuation timing versus later post-apply request shapes
  - Automated proof that remote background apply preserves tail items appended after compaction started
affects: [phase-3-durable-history-and-surface-compatibility, phase-4-failure-recovery-and-blocking-guardrails]
tech-stack:
  added: []
  patterns: [request-shape assertions over timing-sensitive background work, same-turn post-apply tail verification]
key-files:
  created: []
  modified:
    - codex-rs/core/src/codex_tests.rs
    - codex-rs/core/src/context_manager/history_tests.rs
    - codex-rs/core/tests/suite/compact.rs
    - codex-rs/core/tests/suite/compact_remote.rs
key-decisions:
  - "Separated immediate continuation assertions from later post-apply assertions so the test suite reflects real background timing instead of assuming compaction finishes before the next request"
  - "Used a remote same-turn tail function-call scenario to prove Phase 2 preserves items appended after compaction started"
patterns-established:
  - "Phase 2 regressions distinguish pre-apply continuation requests from later requests that observe the applied splice"
requirements-completed: [HIST-01, HIST-02]
duration: 1h 35m
completed: 2026-03-09
---

# Phase 2 Plan 03: Regression Coverage Summary

**Phase 2 transcript splicing is now guarded by direct unit and integration coverage for local and remote apply behavior**

## Performance

- **Duration:** 1h 35m
- **Completed:** 2026-03-09
- **Files modified:** 4

## Accomplishments

- Added `codex-rs/core/src/codex_tests.rs` coverage for successful single-apply splicing and diverged-prefix skip behavior on the turn-owned apply path
- Added `codex-rs/core/src/context_manager/history_tests.rs` coverage for the pure prefix-splice helper’s preserved-tail and mismatch behavior
- Updated `codex-rs/core/tests/suite/compact.rs` so the local background compaction regressions reflect real timing: immediate continuation requests stay unspliced, while Phase 2’s cross-turn local apply path is still covered by the existing follow-up request regression
- Updated `codex-rs/core/tests/suite/compact_remote.rs` so remote regressions distinguish immediate continuation from later applied history and directly prove that a newer same-turn tail function call survives below the applied compaction item

## Task Commits

1. **Task 1: Add splice helper and single-apply invariants** - `e2bc8645d` (`test(core): cover transcript splice behavior`)
2. **Task 2: Add local and remote live transcript ordering regressions** - `e2bc8645d` (`test(core): cover transcript splice behavior`)

## Files Created/Modified

- `codex-rs/core/src/codex_tests.rs` - adds turn-owned apply-path tests for single-apply and mismatch skipping
- `codex-rs/core/src/context_manager/history_tests.rs` - adds pure helper coverage for preserved tail ordering and prefix mismatch
- `codex-rs/core/tests/suite/compact.rs` - updates local timing-sensitive regression assertions for Phase 2 behavior
- `codex-rs/core/tests/suite/compact_remote.rs` - proves remote post-apply history includes the compacted prefix while preserving newer tail items

## Issues Encountered

- The first full `cargo test -p codex-core` pass hit a timeout in `shell_snapshot::tests::snapshot_shell_does_not_inherit_stdin`. Rerunning that test in isolation passed, so this was treated as a harness flake rather than a Phase 2 regression.
- Several original regression assertions assumed background compaction would finish before the immediate continuation request. The final coverage now separates immediate continuation from later post-apply observations explicitly.

## Verification

- `cargo test -p codex-core compact`
- `cargo test -p codex-core`
- `cargo test -p codex-core snapshot_shell_does_not_inherit_stdin`
- `just fix -p codex-core`
- `just fmt`

Note: per repo instructions, tests were not rerun after the final `just fix -p codex-core` / `just fmt` pass.

## Next Phase Readiness

- Phase 3 can validate replay, resume, rollback, and read-path parity against the same splice semantics these tests now cover live
- Phase 4 can add failure fallback on top of a test suite that already guards successful apply timing and tail preservation

---
*Phase: 02-safe-transcript-splicing*
*Completed: 2026-03-09*
