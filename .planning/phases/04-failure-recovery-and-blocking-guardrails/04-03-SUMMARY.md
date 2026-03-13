---
phase: 04-failure-recovery-and-blocking-guardrails
plan: 03
subsystem: core
tags: [rust, core, app-server, compaction, guardrails, testing]
requires:
  - phase: 04-failure-recovery-and-blocking-guardrails
    provides: failure-only background fallback path
provides:
  - blocking guardrail regressions for manual and pre-turn compaction
  - failure-only background completion signaling
  - clarified app-server manual compaction contract
affects: [phase-4, background-compaction, app-server, guardrails]
tech-stack:
  added: []
  patterns: [failure-only background signaling, buffered notification assertions for compaction turns]
key-files:
  created:
    - .planning/phases/04-failure-recovery-and-blocking-guardrails/04-03-SUMMARY.md
  modified:
    - codex-rs/core/src/codex.rs
    - codex-rs/core/src/codex_tests.rs
    - codex-rs/core/src/state/turn.rs
    - codex-rs/core/tests/suite/compact.rs
    - codex-rs/core/tests/suite/compact_remote.rs
    - codex-rs/app-server/tests/suite/v2/compaction.rs
    - codex-rs/app-server/README.md
key-decisions:
  - "Wait only on background failure signals at turn end so successful background compaction stays non-blocking."
  - "Lock manual and pre-turn guardrails with ordering assertions instead of inferring blocking semantics indirectly from request counts."
  - "Document `thread/compact/start` as an immediate RPC response that still owns the thread until the matching `turn/completed` arrives."
patterns-established:
  - "App-server compaction notification tests should consume buffered `item/*` traffic from the triggering turn instead of re-waiting for a turn completion that was already observed."
requirements-completed: [COMP-01, COMP-02]
duration: 19min
completed: 2026-03-09
---

# Phase 4: Failure Recovery And Blocking Guardrails Summary

**Wave 2 finished by locking manual and pre-turn blocking semantics while tightening background recovery so only failures can pause turn teardown.**

## Performance

- **Duration:** 19 min
- **Started:** 2026-03-09T10:08:08-0500
- **Completed:** 2026-03-09T10:27:22-0500
- **Tasks:** 2
- **Files modified:** 7

## Accomplishments

- Refined background recovery signaling so end-of-turn fallback waits only for failed background outcomes, preserving the non-blocking success path.
- Added local and remote guardrail regressions proving manual compaction still blocks through item completion and pre-turn protective compaction failures still finish without separate abort markers.
- Stabilized the app-server local auto-compaction test around buffered `item/started` and `item/completed` notifications from the triggering turn instead of double-waiting for an already-consumed `turn/completed`.
- Clarified the `thread/compact/start` contract in the app-server README: the RPC returns immediately, but the thread remains occupied until the compaction turn completes.

## Task Commits

1. **Task 1-2: Preserve blocking guardrails while keeping successful background compaction non-blocking** - pending commit

## Files Created/Modified

- `codex-rs/core/src/codex.rs` - Switched end-of-turn recovery from generic background completion waits to failure-only signaling.
- `codex-rs/core/src/state/turn.rs` - Renamed background completion tracking to explicit failure notifications on active turns.
- `codex-rs/core/src/codex_tests.rs` - Updated focused state constructors to match the failure-only signaling model.
- `codex-rs/core/tests/suite/compact.rs` - Added direct local guardrail assertions for manual compaction blocking and pre-turn failure semantics.
- `codex-rs/core/tests/suite/compact_remote.rs` - Added the matching remote pre-turn guardrail regression.
- `codex-rs/app-server/tests/suite/v2/compaction.rs` - Tightened manual and local auto-compaction notification ordering assertions.
- `codex-rs/app-server/README.md` - Documented that manual thread compaction still occupies the thread until `turn/completed`.

## Decisions Made

- Kept success-path background compaction entirely out of the new end-of-turn wait path so Phase 4 does not regress the non-interrupting behavior introduced earlier.
- Asserted blocking semantics through event ordering, which is the user-visible contract, rather than through incidental timing or request-count assumptions.

## Deviations from Plan

Minor: preserving the success path required a small core refactor in addition to the planned regression updates. The original broad completion signal made the app-server local auto-compaction path look blocking again during teardown.

## Issues Encountered

- The first app-server regression timed out because the test re-waited for the compaction turn’s `turn/completed` after `send_turn_and_wait(...)` had already consumed it.
- A generic background completion notify briefly made successful background compaction visible to end-of-turn recovery; narrowing it to failure-only notifications resolved that without changing failure fallback behavior.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

Wave 3 can now add the final terminal-outcome regression net on top of a stable failure-only recovery path and locked blocking guardrails.

---
*Phase: 04-failure-recovery-and-blocking-guardrails*
*Completed: 2026-03-09*
