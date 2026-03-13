# Milestones

## v1.0 Async Rolling Auto-Compaction (Shipped: 2026-03-09)

**Phases completed:** 6 phases, 21 plans, 38 tasks

**Key accomplishments:**
- Automatic mid-turn compaction now launches in the background without replacing the active turn.
- Completed background compactions splice only the captured transcript prefix and preserve newer tail items below it.
- Durable replay, read, resume, rollback, and app-server compaction surfaces remain compatible with the new background behavior.
- Failed background compaction falls back through the existing blocking path, while manual and pre-turn guardrails remain blocking.
- Rolling compaction now supports overlap and shows a below-input TUI indicator without transcript chatter.
- Milestone verification artifacts, validation metadata, and the final audit all closed cleanly with `status: passed`.

---
