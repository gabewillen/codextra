# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-03-08)

**Core value:** When an agent is working, compaction never interrupts the agent unless it fails, and I can see compactions happening in an indicator below the input.
**Current focus:** Phase 3 - Durable History And Surface Compatibility

## Current Position

Phase: 3 of 5 (Durable History And Surface Compatibility)
Plan: 2 of 3 in current phase
Status: In progress
Last activity: 2026-03-09 — Completed Wave 2 by making app-server compaction history semantics explicit in protocol tests and docs without changing reducer behavior

Progress: [████░░░░░░] 40%

## Performance Metrics

**Velocity:**
- Total plans completed: 7
- Average duration: 76 min
- Total execution time: 5.1 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 1 | 4/4 | 305 min | 76 min |
| 2 | 3/3 | 235 min | 78 min |
| 3 | 2/3 | 63 min | 32 min |
| 4 | TBD | 0 min | - |
| 5 | TBD | 0 min | - |

**Recent Trend:**
- Last 5 plans: 65m, 65m, 95m, 80m
- Trend: Slightly increasing with runtime/test orchestration work

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- [Phase 1] Start with non-blocking automatic mid-turn compaction before broader rolling concurrency.
- [Phase 1] Split local and remote compaction into snapshot-owned worker/apply helpers before touching active-turn orchestration.
- [Phase 1] Keep synthetic summarize prompts out of returned replacement history so blocking semantics and snapshots stay stable during the refactor.
- [Phase 1] Launch mid-turn auto-compaction as auxiliary turn-owned work and defer all live-history application to Phase 2.
- [Phase 3] Make replay, resume, rollback, and read-flow parity a dedicated phase before UX hardening.
- [Phase 3] Treat persisted spliced `replacement_history` as already durable in core and lock that contract behind rollout-backed tests before touching app-server surfaces.
- [Phase 3] Keep app-server thread history marker-only for persisted compaction checkpoints and document that contract instead of synthesizing extra thread items from `replacement_history`.
- [Phase 5] Land multi-compaction overlap together with the visible background indicator after correctness and recovery work.

### Pending Todos

- Execute Plan 03-03 to add end-to-end regressions across app-server read/resume/rollback surfaces on top of the now-explicit compaction history contract.

### Blockers/Concerns

- Full `cargo test -p codex-core` had one transient `shell_snapshot` timeout on the first run; the isolated rerun passed.

## Session Continuity

Last session: 2026-03-09 14:50 CDT
Stopped at: Phase 3 Wave 2 complete; next up is the final regression wave for app-server read/rollback compatibility
Resume file: None
