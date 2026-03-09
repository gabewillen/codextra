# Codex

## What This Is

Codex is a Rust-first coding agent product with CLI, TUI, exec, SDK, and app-server surfaces built around a shared session and thread engine. This project initialization is for a brownfield enhancement to Codex's existing conversation compaction system: add async rolling auto-compaction so long-running agent work can continue without visible compaction interruptions.

## Core Value

When an agent is working, compaction never interrupts the agent unless it fails, and I can see compactions happening in an indicator below the input.

## Requirements

### Validated

- ✓ Codex supports long-running agent conversations across CLI, TUI, exec, and app-server clients — existing
- ✓ Codex already compacts conversation history automatically and manually to manage context limits — existing
- ✓ Codex persists transcripts and streams thread/item events that clients can render live — existing

### Active

- [ ] Automatic mid-turn compaction runs in the background without interrupting ongoing agent work
- [ ] Completed background compactions replace only the compacted transcript section and leave messages created during compaction below the new compacted top message
- [ ] Multiple auto-compactions can be in flight concurrently on different transcript ranges
- [ ] If background compaction fails, Codex falls back to the existing interrupting compaction flow and waits for recovery to complete
- [ ] The TUI shows a lightweight indicator below the input while background compaction is happening so users can tell it is working

### Out of Scope

- Manual compaction behavior changes — the requested scope is automatic mid-turn compaction only
- Pre-turn safety compaction redesign — existing context-protection behavior should stay intact unless background compaction fails
- Hiding compaction activity entirely — an indicator is required so users can confirm background compaction is active

## Context

Codex already has compaction paths in `codex-rs/core/src/compact.rs`, `codex-rs/core/src/compact_remote.rs`, and `codex-rs/core/src/tasks/compact.rs`, with app-server notification coverage in `codex-rs/app-server/tests/suite/v2/compaction.rs` and core compaction coverage in `codex-rs/core/tests/suite/compact.rs` and `codex-rs/core/tests/suite/compact_remote.rs`. The current behavior interrupts active agent work and waits for compaction to finish before continuing. The desired change is to turn automatic mid-turn compaction into background maintenance that splices finished summaries into the stored transcript while preserving newer messages that arrived during the compaction window.

## Constraints

- **Tech stack**: Implement within the existing Rust session/thread architecture in `codex-rs/core/`, with matching TUI behavior in `codex-rs/tui/` and any necessary app-server/event adjustments — preserve existing product architecture
- **Compatibility**: Manual compaction and pre-turn protective compaction should keep current semantics — this request is limited to auto mid-turn compaction
- **Transcript integrity**: Background compaction must replace the correct transcript slice and preserve ordering of messages created while compaction was running — otherwise history becomes misleading or corrupt
- **Failure handling**: Failed background compactions must stop the agent and fall back to the current blocking recovery path — avoid silently drifting into an over-limit or inconsistent state
- **Visibility**: Users should see continuous agent interaction plus a lightweight compaction indicator below the input — the feature should be observable without transcript interruptions

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Limit v1 scope to automatic mid-turn compaction | User explicitly wants the current auto-compaction interruption removed without broad behavior churn elsewhere | — Pending |
| Allow multiple background compactions in flight | User wants rolling compaction to repeat without waiting for earlier compactions to finish | — Pending |
| Fall back to existing blocking compaction on background failure | Preserves current recovery behavior instead of inventing a new degraded mode | — Pending |
| Show progress in an indicator below the input instead of the transcript | User wants uninterrupted agent interaction while still being able to confirm compaction is happening | — Pending |

---
*Last updated: 2026-03-08 after initialization*
