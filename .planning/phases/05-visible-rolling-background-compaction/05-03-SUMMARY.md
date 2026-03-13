---
phase: 05-visible-rolling-background-compaction
plan: 03
subsystem: tui
tags: [rust, tui, compaction, ux, snapshots]
requires:
  - phase: 05-visible-rolling-background-compaction
    plan: 02
    provides: rolling runtime settlement semantics
provides:
  - below-input compaction indicator
  - overlap-safe TUI activity tracking
  - suppressed successful compaction transcript chatter
affects: [phase-5, tui, footer-indicator]
tech-stack:
  added: []
  patterns:
    - compaction visibility is modeled as footer activity state instead of transcript history
key-files:
  created: []
  modified:
    - codex-rs/tui/src/bottom_pane/chat_composer.rs
    - codex-rs/tui/src/bottom_pane/footer.rs
    - codex-rs/tui/src/bottom_pane/mod.rs
    - codex-rs/tui/src/chatwidget.rs
    - codex-rs/tui/src/chatwidget/tests.rs
    - codex-rs/tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__chat_composer__tests__footer_mode_context_compaction_active.snap
key-decisions:
  - "Track compaction activity as a count so overlapping background compactions keep the indicator visible until the last one finishes."
  - "Suppress successful `ContextCompacted` transcript chatter entirely and use footer-only visibility for background compaction progress."
patterns-established:
  - "Background system work that should stay out of the transcript belongs in dedicated footer activity state with snapshot coverage."
requirements-completed: [VIS-01, VIS-02]
duration: 8min
completed: 2026-03-09
---

# Phase 5: Visible Rolling Background Compaction Summary

**Wave 2 TUI work moved background compaction visibility below the input and removed successful compaction interruptions from the transcript.**

## Performance

- **Duration:** 8 min
- **Completed:** 2026-03-09T11:28:00-0500
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments

- Added a dedicated `Compacting context...` footer line beneath the input instead of showing successful background compaction as transcript chatter.
- Converted compaction activity tracking from a boolean to a counter so overlapping background compactions keep the footer indicator active until the final completion.
- Wired turn cleanup, explicit `ContextCompacted`, and item lifecycle events into the footer activity model so the indicator clears correctly across success, failure, and interruption paths.
- Added chat widget tests covering no-chatter behavior, lifecycle toggling, overlap-safe activity tracking, and the footer snapshot for the new layout.

## Files Created/Modified

- `codex-rs/tui/src/bottom_pane/chat_composer.rs` - Rendered the below-input compaction row and switched footer layout calculations to count-backed activity.
- `codex-rs/tui/src/bottom_pane/footer.rs` - Added the dedicated compaction indicator line helper.
- `codex-rs/tui/src/bottom_pane/mod.rs` - Exposed increment/decrement/clear helpers for compaction activity.
- `codex-rs/tui/src/chatwidget.rs` - Routed compaction lifecycle events to footer activity state and kept `ContextCompacted` out of transcript history.
- `codex-rs/tui/src/chatwidget/tests.rs` - Added no-chatter, overlap, and indicator rendering coverage.
- `codex-rs/tui/src/bottom_pane/snapshots/codex_tui__bottom_pane__chat_composer__tests__footer_mode_context_compaction_active.snap` - Locked the new footer layout.

## Validation

- `cargo test -p codex-tui`
- `cargo insta pending-snapshots --manifest-path tui/Cargo.toml`

## Next Phase Readiness

The final cross-surface lock-in can now treat background compaction as a footer progress signal instead of a transcript event.

---
*Phase: 05-visible-rolling-background-compaction*
*Completed: 2026-03-09*
