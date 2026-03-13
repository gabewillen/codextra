# Project Retrospective

## Milestone: v1.0 - Async Rolling Auto-Compaction

**Shipped:** 2026-03-09
**Phases:** 6
**Plans:** 21
**Sessions:** 1

### What Was Built

- Non-interrupting mid-turn background compaction for active agent work
- Safe transcript splicing that preserves newer tail messages below the compacted prefix
- Durable replay, resume, rollback, read, and app-server compatibility for the new history shape
- Failure fallback, rolling overlap support, and a below-input indicator without successful transcript chatter

### What Worked

- Strict phase boundaries kept runtime behavior, transcript integrity, durability, recovery, UX, and closeout concerns isolated.
- Summary-first execution left enough evidence to backfill verification and traceability cleanly at the end of the milestone.

### What Was Inefficient

- Dedicated GSD execution and verification agent roles were unavailable, so planning and closeout work had to run locally.
- The archive helper did not recover task counts or accomplishments automatically, which required manual cleanup of the live planning surface.

### Patterns Established

- Background compaction should be modeled as turn-owned auxiliary work rather than an interrupting replacement turn.
- Safe apply needs snapshot-backed prefix guards and deterministic settlement when compactions overlap.
- Audit closure requires summaries, verification artifacts, validation metadata, and requirement traceability to stay in sync.

### Key Lessons

- Separating launch, apply, durability, recovery, UX, and archive hygiene into distinct phases kept the product work moving without conflating concerns.
- Skipping verification artifacts during execution creates avoidable milestone debt even when the implementation is already correct.

### Cost Observations

- Model mix: not tracked in milestone artifacts
- Sessions: 1
- Notable: once the phase boundaries were clear, most late effort shifted from product code to archival and verification hygiene
