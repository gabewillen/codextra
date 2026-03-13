---
phase: 01-background-trigger-and-continued-turns
plan: 01
subsystem: infra
tags: [compaction, local, history, background]
requires: []
provides:
  - Local compaction can compute a snapshot-owned result without mutating live session history during worker execution
  - Inline local compaction callers still apply synchronously through the existing replacement path
affects: [01-03, phase-2-safe-transcript-splicing]
tech-stack:
  added: []
  patterns: [worker-apply split for compaction flows]
key-files:
  created: []
  modified: [codex-rs/core/src/compact.rs]
key-decisions:
  - "Kept the worker/apply split entirely inside `compact.rs` so Wave 1 could land without touching shared orchestration code yet"
  - "Recorded local compaction output into a frozen history snapshot, not live session history, to keep the background path Phase 1-safe"
patterns-established:
  - "Local compaction helpers can return a `LocalCompactResult` for later session-owned application"
requirements-completed: [RUN-01]
duration: 1h 05m
completed: 2026-03-09
---

# Phase 1 Plan 01: Local Compaction Worker Split Summary

**Snapshot-owned local compaction results now flow through a separate worker/apply boundary instead of mutating session history inline**

## Performance

- **Duration:** 1h 05m
- **Started:** 2026-03-09T03:15:00Z
- **Completed:** 2026-03-09T04:20:00Z
- **Tasks:** 2
- **Files modified:** 1

## Accomplishments
- Added `LocalCompactResult` and split local compaction into compute/apply steps in `codex-rs/core/src/compact.rs`
- Kept blocking manual and pre-turn local compaction semantics intact by rebuilding inline callers on top of the new result helper
- Added a focused regression test proving result construction uses the frozen history snapshot rather than live session history

## Task Commits

1. **Task 1: Extract a background-safe local compaction worker** - `5bcbcdaf9` (refactor)
2. **Task 2: Rebuild inline local compaction on top of the worker result** - `5bcbcdaf9` (refactor)

**Plan metadata:** `pending` (docs commit follows after shared Wave 1 bookkeeping)

## Files Created/Modified
- `codex-rs/core/src/compact.rs` - adds the local worker/apply split, snapshot-owned output recording, and a frozen-history regression test

## Decisions Made
- Kept all Wave 1 local behavior changes inside `compact.rs` to avoid coupling this plan to the orchestration work in `codex.rs`
- Built the prompt from a prompt-only history clone so the synthesized summarize instruction does not leak into replacement history

## Deviations from Plan

### Auto-fixed Issues

**1. Shared Wave 1 commit due Git index lock**
- **Found during:** plan bookkeeping after both Wave 1 implementations landed
- **Issue:** Parallel plan-scoped commits raced on Git's index lock, so the local and remote file changes landed in one disjoint shared refactor commit
- **Fix:** Kept the shared commit because the file ownership boundaries remained clean and the wave was revalidated with `just fmt`, `cargo test -p codex-core compact`, and `cargo test -p codex-core compact_remote`
- **Files modified:** `codex-rs/core/src/compact.rs`, `codex-rs/core/src/compact_remote.rs`
- **Verification:** Wave 1 tests passed after the integration fix that removed the synthesized compaction prompt from local replacement history
- **Committed in:** `5bcbcdaf9`

---

**Total deviations:** 1 auto-fixed (wave-level Git bookkeeping race)
**Impact on plan:** No scope creep. The history is less granular than intended, but the code change stayed isolated and the wave was revalidated as a unit.

## Issues Encountered
- The first integrated test run exposed snapshot regressions because the synthesized summarize prompt was being recorded into the returned local replacement history. The fix was to build the prompt from a cloned prompt-only history while keeping result construction based on the frozen session snapshot.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- `codex-rs/core/src/codex.rs` can now call a local snapshot-result helper without forcing immediate history mutation
- Phase 2 work can reuse `LocalCompactResult` when the turn runtime starts tracking deferred background compaction results

---
*Phase: 01-background-trigger-and-continued-turns*
*Completed: 2026-03-09*
