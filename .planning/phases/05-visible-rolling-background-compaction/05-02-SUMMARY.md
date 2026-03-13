---
phase: 05-visible-rolling-background-compaction
plan: 02
subsystem: core
tags: [rust, core, compaction, overlap, recovery, testing]
requires:
  - phase: 05-visible-rolling-background-compaction
    plan: 01
    provides: rolling background job tracking and launch ordering
provides:
  - deterministic rolling compaction settlement
  - stale success deferral and stale failure drop behavior
  - single-shot fallback recovery under overlap
affects: [phase-5, rolling-overlap, fallback-guardrails]
tech-stack:
  added: []
  patterns:
    - newer tracked background compactions defer older successful completions until they either finish or disappear
key-files:
  created: []
  modified:
    - codex-rs/core/src/codex.rs
    - codex-rs/core/src/codex_tests.rs
key-decisions:
  - "Only the newest still-relevant completed compaction may apply; older successful completions stay queued while a newer overlapping run is still tracked."
  - "A failed completion only triggers blocking fallback when it is the newest still-relevant background compaction."
patterns-established:
  - "Rolling background compaction settlement should prefer the freshest applicable result and cancel older work once that result applies."
requirements-completed: [RUN-03]
duration: 10min
completed: 2026-03-09
---

# Phase 5: Visible Rolling Background Compaction Summary

**Wave 2 core work made rolling background compactions settle deterministically without weakening Phase 4's fallback guardrails.**

## Performance

- **Duration:** 10 min
- **Completed:** 2026-03-09T11:20:00-0500
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- Replaced the split apply/recover helpers with a single settlement path that consumes completed background compactions in launch order from newest to oldest.
- Deferred older successful completions while a newer overlapping background compaction is still tracked so the older result cannot make the fresher one stale by applying first.
- Kept failure recovery single-shot by only invoking the blocking fallback path for the newest still-relevant failed background compaction.
- Added focused core tests proving older successes stay queued while newer runs exist and that the renamed settlement contract still preserves splice safety and durable history behavior.

## Files Created/Modified

- `codex-rs/core/src/codex.rs` - Unified rolling success/failure settlement, stale-drop behavior, and fallback cancellation logic.
- `codex-rs/core/src/codex_tests.rs` - Updated background compaction tests to the new settle contract and added overlap deferral coverage.

## Decisions Made

- A newer overlapping background compaction always has first claim on the transcript because it summarizes a strictly larger captured prefix.
- Older failed completions are treated as stale if newer background work still exists, preserving a single fallback decision point.

## Issues Encountered

- Matching directly on completed outcomes partially moved the tracked completion before the defer path could requeue it; the defer check now runs before the outcome match.

## Validation

- `cargo test -p codex-core compact`

## Next Phase Readiness

The runtime now has stable rolling-settlement rules, so the TUI can safely reflect overlapping compaction activity without transcript churn.

---
*Phase: 05-visible-rolling-background-compaction*
*Completed: 2026-03-09*
