# Requirements: Codex Async Rolling Auto-Compaction

**Defined:** 2026-03-08
**Core Value:** When an agent is working, compaction never interrupts the agent unless it fails, and I can see compactions happening in an indicator below the input.

## v1 Requirements

### Runtime

- [ ] **RUN-01**: Codex can start automatic mid-turn compaction in the background without interrupting an active agent turn
- [ ] **RUN-02**: User can continue seeing agent progress while background compaction is in progress
- [ ] **RUN-03**: Codex can run multiple automatic background compactions concurrently on different transcript ranges

### History Integrity

- [ ] **HIST-01**: Codex replaces only the transcript section covered by a completed background compaction
- [ ] **HIST-02**: User can see messages created after compaction started remain below the new compacted top message in the correct order
- [ ] **HIST-03**: User sees the same post-compaction transcript across live sessions, resume, rollback, and read flows

### Recovery

- [ ] **RECV-01**: If a background compaction fails, Codex stops the active agent and falls back to the existing blocking compaction flow
- [ ] **RECV-02**: Each background compaction resolves through exactly one terminal outcome: applied, failed-then-fallback, or aborted

### Visibility

- [ ] **VIS-01**: User can see a lightweight indicator below the input while background compaction is active
- [ ] **VIS-02**: User does not see transcript interruption chatter for successful background compactions

### Compatibility

- [ ] **COMP-01**: User-triggered manual compaction keeps its current blocking behavior
- [ ] **COMP-02**: Pre-turn protective compaction keeps its current blocking behavior
- [ ] **COMP-03**: Existing app-server and thread-item compaction flows remain compatible with the new background compaction behavior

## v2 Requirements

### Visibility And Diagnostics

- **DIAG-01**: User can see richer background compaction progress details beyond the lightweight indicator
- **DIAG-02**: Operators can inspect compaction efficiency, failure rates, and summary quality

### Scheduling

- **SCHED-01**: Codex can use predictive or speculative compaction heuristics before current thresholds are hit
- **SCHED-02**: User can tune background compaction policy or queue behavior when defaults are insufficient

## Out of Scope

| Feature | Reason |
|---------|--------|
| Manual `/compact` redesign | This project targets automatic mid-turn compaction only |
| Pre-turn protective compaction redesign | Existing context-protection behavior should remain stable |
| Hiding compaction activity entirely | The user explicitly wants a visible indicator below the input |

## Traceability

| Requirement | Phase | Status |
|-------------|-------|--------|
| RUN-01 | Phase TBD | Pending |
| RUN-02 | Phase TBD | Pending |
| RUN-03 | Phase TBD | Pending |
| HIST-01 | Phase TBD | Pending |
| HIST-02 | Phase TBD | Pending |
| HIST-03 | Phase TBD | Pending |
| RECV-01 | Phase TBD | Pending |
| RECV-02 | Phase TBD | Pending |
| VIS-01 | Phase TBD | Pending |
| VIS-02 | Phase TBD | Pending |
| COMP-01 | Phase TBD | Pending |
| COMP-02 | Phase TBD | Pending |
| COMP-03 | Phase TBD | Pending |

**Coverage:**
- v1 requirements: 13 total
- Mapped to phases: 0
- Unmapped: 13 ⚠️

---
*Requirements defined: 2026-03-08*
*Last updated: 2026-03-08 after initial definition*
