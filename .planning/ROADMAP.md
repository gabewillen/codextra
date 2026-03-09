# Roadmap: Codex Async Rolling Auto-Compaction

## Overview

This first roadmap takes Codex from interrupting automatic compaction to background rolling auto-compaction for long-running agent work. The sequence follows the v1 requirements directly: start with non-blocking runtime behavior, make transcript splicing safe, preserve durable cross-surface history, keep failure recovery and blocking compatibility intact, then expose visible rolling compaction with concurrent background jobs.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

- [x] **Phase 1: Background Trigger And Continued Turns** - Start automatic mid-turn compaction in the background while agent output continues.
- [ ] **Phase 2: Safe Transcript Splicing** - Apply completed background compactions only to the captured transcript slice and preserve newer messages.
- [ ] **Phase 3: Durable History And Surface Compatibility** - Keep post-compaction history consistent across persistence, replay, and existing compaction surfaces.
- [ ] **Phase 4: Failure Recovery And Blocking Guardrails** - Recover failed background jobs through the existing blocking path without changing manual or pre-turn semantics.
- [ ] **Phase 5: Visible Rolling Background Compaction** - Show lightweight background compaction status and support multiple concurrent auto-compactions.

## Phase Details

### Phase 1: Background Trigger And Continued Turns
**Goal**: Codex starts automatic mid-turn compaction in the background without interrupting the active agent turn.
**Depends on**: Nothing (first phase)
**Requirements**: [RUN-01, RUN-02]
**Success Criteria** (what must be TRUE):
1. User can keep a long-running agent turn active when automatic compaction is triggered.
2. User continues seeing agent progress while background compaction is in flight.
3. Automatic mid-turn compaction starts without requiring any new user action.
**Plans**: 4 plans

### Phase 2: Safe Transcript Splicing
**Goal**: Completed background compactions replace only the intended transcript slice and preserve newer history in order.
**Depends on**: Phase 1
**Requirements**: [HIST-01, HIST-02]
**Success Criteria** (what must be TRUE):
1. After background compaction finishes, only the targeted transcript section is replaced.
2. Messages created after compaction started remain below the new compacted top message in the correct order.
3. User does not see duplicated, reordered, or dropped messages after a compaction is applied.
**Plans**: TBD during phase planning

### Phase 3: Durable History And Surface Compatibility
**Goal**: Async compaction results stay durable and compatible across live sessions, replay flows, and existing compaction surfaces.
**Depends on**: Phase 2
**Requirements**: [HIST-03, COMP-03]
**Success Criteria** (what must be TRUE):
1. User sees the same post-compaction transcript in live sessions, resume, rollback, and read flows.
2. Existing app-server and thread-item compaction consumers continue to work with background compaction enabled.
3. Persisted async compaction results replay into the same transcript structure that live users saw when compaction completed.
**Plans**: TBD during phase planning

### Phase 4: Failure Recovery And Blocking Guardrails
**Goal**: Failed background compactions recover through the existing blocking path while manual and pre-turn compaction semantics stay unchanged.
**Depends on**: Phase 3
**Requirements**: [RECV-01, RECV-02, COMP-01, COMP-02]
**Success Criteria** (what must be TRUE):
1. If a background compaction fails, the active agent stops and Codex completes recovery through the existing blocking compaction flow.
2. Each background compaction reaches exactly one terminal outcome: applied, failed-then-fallback, or aborted.
3. User-triggered manual compaction still behaves as a blocking operation.
4. Pre-turn protective compaction still behaves as a blocking operation.
**Plans**: TBD during phase planning

### Phase 5: Visible Rolling Background Compaction
**Goal**: Codex makes background compaction visible below the input and supports multiple concurrent auto-compactions on different transcript ranges.
**Depends on**: Phase 4
**Requirements**: [VIS-01, VIS-02, RUN-03]
**Success Criteria** (what must be TRUE):
1. User can see a lightweight indicator below the input whenever background compaction is active.
2. Successful background compactions do not add interruption chatter to the transcript.
3. Codex can keep multiple automatic background compactions in flight concurrently on different transcript ranges.
4. The indicator stays accurate as background compactions start, complete, and fail.
**Plans**: TBD during phase planning

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Background Trigger And Continued Turns | 4/4 | Complete | 2026-03-09 |
| 2. Safe Transcript Splicing | 0/TBD | Not started | - |
| 3. Durable History And Surface Compatibility | 0/TBD | Not started | - |
| 4. Failure Recovery And Blocking Guardrails | 0/TBD | Not started | - |
| 5. Visible Rolling Background Compaction | 0/TBD | Not started | - |
