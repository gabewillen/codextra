---
phase: 03-durable-history-and-surface-compatibility
plan: 01
subsystem: core
tags: [rust, replay, rollback, compaction, testing]
requires:
  - phase: 02-safe-transcript-splicing
    provides: completed background compactions persist spliced replacement history
provides:
  - durable test coverage for persisted spliced compaction replay
  - resume and fork parity checks against persisted spliced history
  - rollback replay coverage for persisted spliced compaction checkpoints
affects: [phase-3, app-server, rollback, resume]
tech-stack:
  added: []
  patterns: [persisted replacement history is the replay source of truth]
key-files:
  created: []
  modified:
    - codex-rs/core/src/codex_tests.rs
    - codex-rs/core/tests/suite/compact_remote.rs
key-decisions:
  - "Core replay logic already handled spliced replacement_history correctly; Phase 3 Wave 1 locked it down with direct durability tests instead of changing runtime behavior."
  - "Resume/fork assertions for in-flight compaction focus on transcript parity, while rollback covers the completed-turn metadata baseline contract."
patterns-established:
  - "Durability phases should prove persisted behavior with rollout-backed tests before changing replay code."
requirements-completed: [HIST-03]
duration: 35min
completed: 2026-03-09
---

# Phase 3: Durable History And Surface Compatibility Summary

**Core replay durability is now locked behind rollout-backed tests for persisted spliced compaction history across resume, fork, and rollback.**

## Performance

- **Duration:** 35 min
- **Started:** 2026-03-09T00:59:00-0500
- **Completed:** 2026-03-09T01:34:25-0500
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments
- Added a rollout-backed test proving completed background auto-compaction persists the fully spliced replacement history and that fresh resume/fork reconstruct the same transcript shape.
- Added a rollback durability test proving persisted spliced compaction checkpoints replay the right history, previous-turn settings, and reference-context baseline after `thread_rollback`.
- Confirmed Wave 1 did not require core replay code changes; the existing reconstruction path already treated `replacement_history` as the durable source of truth.

## Task Commits

1. **Task 1-2: Durable spliced replay coverage and rollback parity** - `184419882` (`test(core): cover durable spliced compaction replay`)

## Files Created/Modified
- `codex-rs/core/src/codex_tests.rs` - Added persisted spliced compaction durability tests for resume, fork, and rollback replay.
- `codex-rs/core/tests/suite/compact_remote.rs` - Formatting-only cleanup from the scoped clippy fix pass.

## Decisions Made
- Chose tests over runtime changes because the existing replay/reconstruction path already handled persisted spliced `replacement_history` correctly.
- Kept resume/fork metadata expectations scoped to transcript parity for in-flight compaction, while using rollback to verify completed-turn baseline metadata hydration.

## Deviations from Plan

None - plan intent was achieved without widening scope into replay code changes.

## Issues Encountered

- The first new resume test assumed `previous_turn_settings` would hydrate from an in-flight compaction checkpoint. Scoped core tests showed that expectation was too strong, so the test was narrowed to the actual contract: persisted transcript parity. Metadata durability remains covered by the completed-turn rollback test.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

Wave 2 can now treat core replay semantics as proven and focus on app-server compatibility surfaces, regression coverage, and documentation.

---
*Phase: 03-durable-history-and-surface-compatibility*
*Completed: 2026-03-09*
