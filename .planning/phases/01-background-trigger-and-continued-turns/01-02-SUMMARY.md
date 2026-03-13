---
phase: 01-background-trigger-and-continued-turns
plan: 02
subsystem: infra
tags: [compaction, remote, history, background]
requires: []
provides:
  - Remote compaction can compute a snapshot-owned result without mutating live session history during worker execution
  - Inline remote compaction callers still apply synchronously through the existing replacement path
affects: [01-03, phase-2-safe-transcript-splicing, app-server]
tech-stack:
  added: []
  patterns: [worker-apply split for compaction flows]
key-files:
  created: []
  modified: [codex-rs/core/src/compact_remote.rs]
key-decisions:
  - "Kept remote request semantics intact and split only at the session-owned apply boundary"
  - "Reused the existing processed-history filtering path so the future background launcher and current inline callers share one normalization flow"
patterns-established:
  - "Remote compaction helpers can return a `RemoteCompactionResult` for later session-owned application"
requirements-completed: [RUN-01]
duration: 1h 05m
completed: 2026-03-09
---

# Phase 1 Plan 02: Remote Compaction Worker Split Summary

**Remote compaction now produces a snapshot-owned result that can be applied separately from the current inline lifecycle**

## Performance

- **Duration:** 1h 05m
- **Started:** 2026-03-09T03:15:00Z
- **Completed:** 2026-03-09T04:20:00Z
- **Tasks:** 2
- **Files modified:** 1

## Accomplishments
- Added `RemoteCompactionResult` plus `run_remote_compact_worker` / `apply_remote_compact_result` in `codex-rs/core/src/compact_remote.rs`
- Rewired existing remote compaction entry points to preserve current compaction item lifecycle while synchronously applying the returned result
- Preserved remote request semantics, processed-history filtering, initial-context injection, and ghost snapshot retention for later background orchestration

## Task Commits

1. **Task 1: Extract a background-safe remote compaction worker** - `5bcbcdaf9` (refactor)
2. **Task 2: Rebuild inline remote compaction on top of the worker result** - `5bcbcdaf9` (refactor)

**Plan metadata:** `pending` (docs commit follows after shared Wave 1 bookkeeping)

## Files Created/Modified
- `codex-rs/core/src/compact_remote.rs` - adds the remote worker/apply split while preserving existing remote compact request behavior

## Decisions Made
- Split the remote path at the session-owned apply step instead of changing transport/request behavior
- Kept `process_compacted_history` as the shared post-processing path so background and inline remote compaction normalize history identically

## Deviations from Plan

### Auto-fixed Issues

**1. Shared Wave 1 commit due Git index lock**
- **Found during:** plan bookkeeping after both Wave 1 implementations landed
- **Issue:** Parallel plan-scoped commits raced on Git's index lock, so the local and remote file changes landed in one disjoint shared refactor commit
- **Fix:** Kept the shared commit because the file ownership boundaries remained clean and the wave was revalidated with `just fmt`, `cargo test -p codex-core compact`, and `cargo test -p codex-core compact_remote`
- **Files modified:** `codex-rs/core/src/compact.rs`, `codex-rs/core/src/compact_remote.rs`
- **Verification:** Wave 1 tests passed after the integrated local-history fix, and the explicit remote compaction suite passed cleanly
- **Committed in:** `5bcbcdaf9`

---

**Total deviations:** 1 auto-fixed (wave-level Git bookkeeping race)
**Impact on plan:** No scope creep. The shared commit is a bookkeeping imperfection, not a design change.

## Issues Encountered
- The initial remote verification attempt was blocked by local compile errors from the integrated Wave 1 refactor, not by issues inside `compact_remote.rs`. Once the local prompt-history fix landed, the remote suite passed unchanged.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- `codex-rs/core/src/codex.rs` can call a remote snapshot-result helper without forcing immediate session mutation
- Phase 2 and app-server compatibility work can reuse `RemoteCompactionResult` and the unchanged normalization path

---
*Phase: 01-background-trigger-and-continued-turns*
*Completed: 2026-03-09*
