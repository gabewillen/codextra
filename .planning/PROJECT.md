# Codex

## What This Is

Codex is a Rust-first coding agent product with CLI, TUI, exec, SDK, and app-server surfaces built around a shared session and thread engine. v1.0 shipped an async rolling auto-compaction upgrade so long-running agent work can continue without visible compaction interruptions, while preserving transcript integrity and existing blocking recovery semantics.

## Core Value

When an agent is working, compaction never interrupts the agent unless it fails, and I can see compactions happening in an indicator below the input.

## Requirements

### Validated

- ✓ Codex supports long-running agent conversations across CLI, TUI, exec, and app-server clients — existing
- ✓ Codex already compacts conversation history automatically and manually to manage context limits — existing
- ✓ Codex persists transcripts and streams thread/item events that clients can render live — existing
- ✓ Automatic mid-turn compaction runs in the background without interrupting ongoing agent work — shipped in v1.0
- ✓ Completed background compactions replace only the compacted transcript section and leave messages created during compaction below the new compacted top message — shipped in v1.0
- ✓ Multiple auto-compactions can be in flight concurrently on different transcript ranges — shipped in v1.0
- ✓ If background compaction fails, Codex falls back to the existing interrupting compaction flow and waits for recovery to complete — shipped in v1.0
- ✓ The TUI shows a lightweight indicator below the input while background compaction is happening so users can tell it is working — shipped in v1.0

### Active

- [ ] Show richer background compaction progress details beyond the lightweight footer indicator
- [ ] Add operator-facing compaction diagnostics for efficiency, failure rate, and summary quality
- [ ] Explore predictive or speculative compaction heuristics before current thresholds are hit
- [ ] Allow tuning of background compaction policy or queue behavior when defaults are insufficient

### Out of Scope

- Manual compaction redesign unless a future milestone explicitly reopens it
- Pre-turn protective compaction redesign unless future reliability work justifies it
- Large live planning surfaces that duplicate archived milestone details

## Context

Codex now ships background auto-compaction across core, TUI, and app-server surfaces. The v1.0 milestone closed with a passed audit on 2026-03-09, archived planning artifacts under `.planning/milestones/`, and left the live planning surface ready for the next milestone. The current Rust codebase is roughly 879k lines, so future work should stay narrowly scoped and preserve the contracts locked in by the shipped compaction tests.

## Current State

- v1.0 Async Rolling Auto-Compaction shipped on 2026-03-09
- Automatic mid-turn compaction is non-interrupting on success and interrupting only on failure fallback
- Completed background compactions splice the captured prefix while preserving newer tail history
- The TUI shows a below-input compaction indicator without success chatter in the transcript
- Planning artifacts for the shipped milestone live in `.planning/milestones/`

## Next Milestone Goals

- Decide whether the next milestone should focus on diagnostics, scheduling controls, or a separate product capability
- Turn the archived v2 ideas into milestone-scoped requirements only after prioritization
- Keep the live roadmap and state files small, current, and limited to active work

## Constraints

- **Tech stack**: Implement within the existing Rust session/thread architecture in `codex-rs/core/`, with matching TUI behavior in `codex-rs/tui/` and any necessary app-server/event adjustments — preserve existing product architecture
- **Compatibility**: Manual compaction and pre-turn protective compaction should keep current semantics — this request is limited to auto mid-turn compaction
- **Transcript integrity**: Background compaction must replace the correct transcript slice and preserve ordering of messages created while compaction was running — otherwise history becomes misleading or corrupt
- **Failure handling**: Failed background compactions must stop the agent and fall back to the current blocking recovery path — avoid silently drifting into an over-limit or inconsistent state
- **Visibility**: Users should see continuous agent interaction plus a lightweight compaction indicator below the input — the feature should be observable without transcript interruptions

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Limit v1 scope to automatic mid-turn compaction | User explicitly wants the current auto-compaction interruption removed without broad behavior churn elsewhere | ✓ Good — shipped in v1.0 without reopening manual or pre-turn semantics |
| Allow multiple background compactions in flight | User wants rolling compaction to repeat without waiting for earlier compactions to finish | ✓ Good — shipped in v1.0 with ordered overlap settlement |
| Fall back to existing blocking compaction on background failure | Preserves current recovery behavior instead of inventing a new degraded mode | ✓ Good — shipped in v1.0 with single-shot fallback |
| Show progress in an indicator below the input instead of the transcript | User wants uninterrupted agent interaction while still being able to confirm compaction is happening | ✓ Good — shipped in v1.0 with a footer indicator and no successful transcript chatter |

---
*Last updated: 2026-03-09 after v1.0 milestone*
