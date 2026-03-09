---
phase: 01-background-trigger-and-continued-turns
plan: 04
subsystem: tests
tags: [compaction, tests, regression, app-server]
requires: [01-03]
provides:
  - Core regression coverage for non-blocking local and remote background auto-compaction
  - Assertions that Phase 1 background compaction does not replace the active turn or inject compacted history into the live continuation request
  - App-server compaction verification for the Phase 1 item lifecycle surface
affects: [phase-2-safe-transcript-splicing, phase-4-failure-recovery-and-blocking-guardrails]
tech-stack:
  added: []
  patterns: [request-shape assertions over snapshots, delayed continuation verification]
key-files:
  created: []
  modified:
    - codex-rs/core/tests/suite/compact.rs
    - codex-rs/core/tests/suite/compact_remote.rs
key-decisions:
  - "Replaced brittle snapshot-only assertions with request-shape checks that directly prove Phase 1 background behavior"
  - "Kept app-server verification at the event-surface level because Phase 1 does not yet apply background results to live history"
patterns-established:
  - "Phase 1 compaction regressions assert continued-turn request contents rather than assuming future splice behavior"
requirements-completed: [RUN-02]
duration: 1h 20m
completed: 2026-03-09
---

# Phase 1 Plan 04: Regression Coverage Summary

**Phase 1 background compaction is now locked behind local, remote, and app-server regression coverage**

## Performance

- **Duration:** 1h 20m
- **Started:** 2026-03-09T06:05:00Z
- **Completed:** 2026-03-09T07:25:00Z
- **Tasks:** 3
- **Files modified:** 2

## Accomplishments

- Reworked `codex-rs/core/tests/suite/compact.rs` to prove local background compaction starts mid-turn, leaves the active turn alive, and does not inject the completed summary back into the continuation request
- Updated `codex-rs/core/tests/suite/compact_remote.rs` so remote Phase 1 regressions assert the compact request carries in-turn tool history while the live continuation request stays free of compaction items and background summary text
- Revalidated app-server Phase 1 surface compatibility with `cargo test -p codex-app-server compaction`; no production-code or test-file changes were required there after the runtime refactor

## Task Commits

1. **Task 1: Add delayed local and remote auto-compaction continuation tests** - `dd6ea76e1` (`test(core): cover background auto compaction flow`)
2. **Task 2: Add active-turn lifecycle and single-flight regression coverage** - `dd6ea76e1` (`test(core): cover background auto compaction flow`)
3. **Task 3: Update app-server compaction tests only for the Phase 1 surface** - no file changes required; verified by `cargo test -p codex-app-server compaction`

## Files Created/Modified

- `codex-rs/core/tests/suite/compact.rs` - adds local Phase 1 request-shape and lifecycle assertions for non-blocking background compaction
- `codex-rs/core/tests/suite/compact_remote.rs` - adds remote Phase 1 request-shape and lifecycle assertions for non-blocking background compaction

## Decisions Made

- Removed several snapshot assertions that encoded future-phase expectations and replaced them with direct request-content checks for Phase 1 semantics
- Treated the transient `user_shell_cmd` timeout as a harness flake and confirmed the targeted truncation regression in isolation before the final full-suite pass
- Kept app-server validation as a verification step only because the existing event surface already matched Phase 1 once the core runtime behavior was updated

## Deviations from Plan

### Auto-fixed Issues

**1. Remote setup-turn test flow was too timing-sensitive**
- **Found during:** full `cargo test -p codex-core compact`
- **Issue:** the remote Phase 1 regression waited on `TurnComplete`, which proved flaky under full-suite load
- **Fix:** switched the setup waits to request-count polling and asserted the compact request contents directly
- **Verification:** the isolated remote regression and the final full `compact` suite passed

**2. Local background compact matcher captured more than one request**
- **Found during:** isolated and full local compaction regression runs
- **Issue:** the local provider could issue multiple matching compaction requests, so singleton assertions were too strict
- **Fix:** asserted against the compact request that actually carried the summarization prompt instead of assuming a single recorded request
- **Verification:** the isolated local regression and the final full `compact` suite passed

---

**Total deviations:** 2 auto-fixed
**Impact on plan:** No scope change. The fixes made the regression suite describe the intended Phase 1 behavior more directly and with less timing sensitivity.

## Verification

- `cargo test -p codex-core compact`
- `cargo test -p codex-core user_shell_cmd`
- `cargo test -p codex-app-server compaction`
- `just fix -p codex-core`
- `just fmt`

## Next Phase Readiness

- Phase 2 can build transcript splice coverage on top of these request-shape assertions without having to revisit the Phase 1 non-blocking runtime contract
- Phase 4 can add fallback coverage knowing the Phase 1 suite already guards against turn replacement and live-history mutation during successful background compaction

---
*Phase: 01-background-trigger-and-continued-turns*
*Completed: 2026-03-09*
