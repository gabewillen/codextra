---
phase: 02-safe-transcript-splicing
plan: 02
subsystem: runtime
tags: [compaction, runtime, transcript, apply]
requires: [02-01]
provides:
  - Completed background auto-compaction results are applied exactly once on the active-turn runtime path
  - Successful local and remote background outcomes reuse the existing compacted-history replacement path
  - Diverged live history skips application instead of forcing an unsafe replacement
affects: [02-03, phase-3-durable-history-and-surface-compatibility, phase-4-failure-recovery-and-blocking-guardrails]
tech-stack:
  added: []
  patterns: [turn-owned completed-result apply hook, shared local-and-remote replacement contract]
key-files:
  created: []
  modified:
    - codex-rs/core/src/codex.rs
    - codex-rs/core/src/compact.rs
    - codex-rs/core/src/compact_remote.rs
    - codex-rs/core/src/state/turn.rs
key-decisions:
  - "Applied completed background compactions only from safe points in the active-turn loop instead of mutating history from the detached worker"
  - "Rewrote CompactedItem replacement history to the fully spliced live history so later replay and rollout consumers see the same ordering the user saw"
patterns-established:
  - "Background compaction apply logic shares one live-history contract for local and remote results"
requirements-completed: [HIST-01, HIST-02]
duration: 1h 20m
completed: 2026-03-09
---

# Phase 2 Plan 02: Turn-Owned Apply Path Summary

**Completed background compactions now splice into live history on the main turn path without dropping newer transcript items**

## Performance

- **Duration:** 1h 20m
- **Completed:** 2026-03-09
- **Files modified:** 4

## Accomplishments

- Added `apply_completed_background_auto_compact_if_ready(...)` in `codex-rs/core/src/codex.rs` and invoked it from safe points in the turn loop so completed background compactions can apply without racing the detached worker
- Spliced completed replacement history into current live history only when the captured snapshot still matches the live prefix; mismatches now warn and skip instead of mutating the transcript
- Reused `replace_compacted_history(...)` and `recompute_token_usage(...)` for both local and remote background outcomes after rebuilding the full spliced history
- Exposed the compact result fields needed for the shared apply path in `codex-rs/core/src/compact.rs` and `codex-rs/core/src/compact_remote.rs`

## Task Commits

1. **Task 1: Wire completed background results into the active-turn runtime** - `b36c7358e` (`feat(core): splice completed background compactions`)
2. **Task 2: Reuse the existing compaction replacement path for local and remote outcomes** - `b36c7358e` (`feat(core): splice completed background compactions`)

## Files Created/Modified

- `codex-rs/core/src/codex.rs` - applies completed background compactions through turn-owned safe points and skips diverged prefixes
- `codex-rs/core/src/compact.rs` - exposes local compact result fields for the shared apply path
- `codex-rs/core/src/compact_remote.rs` - exposes remote compact result fields for the shared apply path
- `codex-rs/core/src/state/turn.rs` - adds helpers for consuming only successful completed background outcomes

## Decisions Made

- Kept failed background outcomes out of the new apply path so Phase 2 stays scoped to safe successful splices; failure fallback remains Phase 4 work
- Applied the fully spliced history to `CompactedItem.replacement_history` so persisted rollout state matches the live transcript order after apply

## Deviations from Plan

None.

## Verification

- `cargo test -p codex-core compact`

## Next Phase Readiness

- Plan 03 can assert direct single-apply, mismatch-guard, and preserved-tail behavior without further runtime restructuring
- Phase 3 can build persistence and replay parity on top of the same spliced replacement history users now see live

---
*Phase: 02-safe-transcript-splicing*
*Completed: 2026-03-09*
