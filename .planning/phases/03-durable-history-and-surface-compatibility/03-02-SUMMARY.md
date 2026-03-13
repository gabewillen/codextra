---
phase: 03-durable-history-and-surface-compatibility
plan: 02
subsystem: api
tags: [rust, app-server, protocol, compaction, docs]
requires:
  - phase: 03-01
    provides: proven core replay durability for persisted spliced compaction history
provides:
  - explicit app-server compaction history contract
  - protocol regression coverage for marker-only replacement history semantics
  - README guidance for thread/read compaction behavior
affects: [phase-3, thread-read, thread-resume, rollback]
tech-stack:
  added: []
  patterns: [app-server thread history follows persisted turn-item stream, not synthesized replacement history]
key-files:
  created: []
  modified:
    - codex-rs/app-server-protocol/src/protocol/thread_history.rs
    - codex-rs/app-server/README.md
key-decisions:
  - "Keep app-server compaction behavior marker-only: persisted replacement_history remains replay metadata for core, not synthesized thread items."
  - "Document thread/read semantics explicitly instead of changing reducer behavior in Phase 3."
patterns-established:
  - "Compatibility waves can lock contracts by testing and documentation when the existing reducer already matches the desired semantics."
requirements-completed: [HIST-03, COMP-03]
duration: 28min
completed: 2026-03-09
---

# Phase 3: Durable History And Surface Compatibility Summary

**App-server thread history now has an explicit compaction contract: persisted checkpoints stay marker-only, and clients consume the persisted turn-item stream rather than synthesized replacement-history text.**

## Performance

- **Duration:** 28 min
- **Started:** 2026-03-09T01:35:00-0500
- **Completed:** 2026-03-09T02:03:00-0500
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments
- Added protocol-level tests proving `Compacted.replacement_history` does not create extra `ThreadItem`s and that compaction-only turns survive whether replacement history is present or not.
- Documented `thread/read(includeTurns)` semantics so persisted compaction checkpoints are explicitly described as turn-item/marker history rather than hidden transcript reconstruction.
- Verified the shared app-server surfaces with scoped `thread_history`, `compaction`, and `thread_rollback` test runs without changing runtime reducer behavior.

## Task Commits

1. **Task 1-2: App-server compaction contract tests and docs** - `919e6b978` (`test(app-server): lock compaction history contract`)

## Files Created/Modified
- `codex-rs/app-server-protocol/src/protocol/thread_history.rs` - Added compaction contract tests and clarified why persisted replacement history remains marker-only in thread history.
- `codex-rs/app-server/README.md` - Documented how `thread/read(includeTurns)` surfaces compaction markers and compaction-only turns.

## Decisions Made
- Preserved the existing reducer behavior because `thread/read`, `thread/resume`, and rollback already share `build_turns_from_rollout_items`; changing that behavior in Phase 3 would have widened scope and risked client drift.
- Made the contract explicit in tests and docs so future phases can change behavior intentionally rather than by accident.

## Deviations from Plan

None - the plan intent was met without touching `codex_message_processor.rs`.

## Issues Encountered

- The new protocol test initially used fields on `ContextCompactedEvent` that do not exist in this codebase. The test was corrected to use the unit struct shape and the scoped protocol suite passed immediately after.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

Wave 3 can now add end-to-end app-server regressions on top of an explicit reducer contract instead of inferring compaction behavior from existing notifications.

---
*Phase: 03-durable-history-and-surface-compatibility*
*Completed: 2026-03-09*
