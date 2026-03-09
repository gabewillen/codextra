# Feature Research

**Domain:** Brownfield agent-runtime enhancement for async rolling auto-compaction
**Researched:** 2026-03-09
**Confidence:** HIGH

## Feature Landscape

This is not a standalone product. The feature sits inside the existing Codex runtime so the bar is preserving current thread correctness and cross-client behavior in `codex-rs/core/`, while adding a better long-running experience for TUI users in `codex-rs/tui/`.

### Table Stakes (Users Expect These)

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Non-blocking mid-turn auto-compaction | Long-running agent work should not pause just because context maintenance starts | HIGH | Current auto-compaction runs inline from `run_auto_compact` in `codex-rs/core/src/codex.rs`; launch needs background scheduling rather than interrupting the active turn |
| Transcript-safe replacement of only the compacted range | Users expect history to remain truthful even if messages arrive while compaction is running | HIGH | Existing replacement is all-at-once via `replace_compacted_history` in `codex-rs/core/src/codex.rs`; async rollout needs range anchoring and splice rules |
| Failure fallback to the current blocking recovery path | Brownfield enhancements cannot silently leave the thread over limit or inconsistent | MEDIUM | Reuse the current local and remote recovery paths in `codex-rs/core/src/compact.rs` and `codex-rs/core/src/compact_remote.rs` instead of inventing a new degraded mode |
| Lightweight visible progress indicator | Users need proof compaction is happening without transcript noise | MEDIUM | Best insertion point is the bottom-pane/status path in `codex-rs/tui/src/bottom_pane/footer.rs`; `codex-rs/tui/src/chatwidget.rs` currently only renders a generic "Context compacted" message |
| Cross-surface behavioral parity | CLI, TUI, exec, and app-server already share the same thread engine and cannot diverge on history semantics | HIGH | Keep core behavior in `codex-rs/core/src/codex.rs`; TUI adds presentation only, while app-server continues emitting thread items from `codex-rs/app-server-protocol/src/protocol/v2.rs` |
| Manual and pre-turn compaction compatibility | Existing `/compact` and protective compaction flows should keep current semantics | MEDIUM | The project scope explicitly keeps manual compaction and pre-turn protection stable; guard this in `codex-rs/core/tests/suite/compact.rs` and `codex-rs/core/tests/suite/compact_remote.rs` |

### Differentiators (Competitive Advantage)

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| Rolling compaction while the agent continues working | Makes Codex feel uninterrupted during long tool-heavy sessions instead of "stop, summarize, resume" | HIGH | This is the core value from `.planning/PROJECT.md`; the turn loop in `codex-rs/core/src/codex.rs` is the critical seam |
| Multiple background compactions on different transcript ranges | Prevents a single large thread from stalling on one oversized maintenance cycle | HIGH | Existing tests already validate repeated auto-compaction in one task in `codex-rs/core/tests/suite/compact.rs`; v1 should extend that into true overlapping-range scheduling only if transcript anchoring is reliable |
| Precise splice behavior that preserves newer messages below the new summary | Users keep a coherent transcript instead of seeing fresh work disappear into a stale summary | HIGH | `replacement_history` already exists on `CompactedItem`; the feature can build on that rather than inventing a second transcript model |
| Observable but low-noise UI | A subtle footer indicator is better than injecting status chatter into transcript history | LOW | Prefer the bottom pane in `codex-rs/tui/src/bottom_pane/footer.rs` or `codex-rs/tui/src/bottom_pane/chat_composer.rs`, with snapshot coverage in `codex-rs/tui/src/chatwidget/tests.rs` |
| Reuse of existing item lifecycle events | Lowers integration risk for IDE and app-server clients because compaction is already modeled as `ContextCompaction` items | MEDIUM | `codex-rs/protocol/src/items.rs`, `codex-rs/app-server/src/bespoke_event_handling.rs`, and `codex-rs/app-server/tests/suite/v2/compaction.rs` already carry the event vocabulary |

### Anti-Features (Commonly Requested, Often Problematic)

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|-----------------|-------------|
| Full transcript suppression of compaction | Feels cleaner on the surface | Conflicts with the explicit requirement for user-visible activity and makes failures harder to diagnose | Keep the transcript quiet but show a footer/status indicator in `codex-rs/tui/src/bottom_pane/footer.rs` |
| Reworking manual `/compact` as part of this project | Seems like "one compaction system" would be simpler | Expands scope into command semantics, UX, and API churn unrelated to the async mid-turn problem | Leave manual compaction on the current path in `codex-rs/core/src/compact.rs` and ship async behavior only for automatic mid-turn compaction |
| User-facing controls for compaction job queues, priorities, or range picking | Sounds powerful for advanced users | Adds configuration burden and creates more states to test across TUI, exec, and app-server | Keep scheduling internal; expose only clear status and consistent fallback behavior |
| Aggressive speculative compaction before thresholds | Promises smoother operation | Risks unnecessary churn, extra model calls, and confusing summary frequency before the current safety threshold is actually hit | Use the existing token-threshold triggers in `codex-rs/core/src/codex.rs` first, then tune heuristics later if evidence demands it |
| Streaming compaction progress into the transcript | Gives detailed visibility | Pollutes conversation history and makes replay/resume harder in already-fragile UI paths like `codex-rs/tui/src/app.rs` and `codex-rs/tui/src/chatwidget.rs` | Emit coarse lifecycle state and render progress out-of-band |

## Feature Dependencies

```text
[Async mid-turn auto-compaction]
    └──requires──> [Range-aware transcript splice]
                       └──requires──> [Stable compaction anchors in session history]

[Async mid-turn auto-compaction]
    └──requires──> [Failure fallback to blocking compaction]

[TUI compaction indicator]
    └──requires──> [Background compaction state events]

[Concurrent rolling compactions]
    └──requires──> [Range-aware transcript splice]
    └──conflicts──> [Single global mutable "current compaction" state]

[Cross-client parity]
    └──requires──> [Core-owned semantics, UI-owned presentation]
```

### Dependency Notes

- **Async mid-turn auto-compaction requires range-aware transcript splice:** current `replace_compacted_history` behavior assumes a single immediate replacement, so async work needs durable knowledge of which slice was summarized.
- **Range-aware transcript splice requires stable compaction anchors in session history:** without anchors tied to the history snapshot taken at compaction start, overlapping jobs can overwrite newer content incorrectly.
- **Async mid-turn auto-compaction requires failure fallback to blocking compaction:** the current synchronous path is the safety net that preserves correctness when background compaction fails.
- **TUI compaction indicator requires background compaction state events:** the footer cannot guess; it needs explicit "in flight" state from core or protocol events rather than inferring from token counts.
- **Concurrent rolling compactions conflict with a single mutable compaction state:** v1 should avoid any design that stores only one active compaction job on the thread because the requirements explicitly call for multiple in-flight jobs on different ranges.
- **Cross-client parity requires core-owned semantics and UI-owned presentation:** transcript mutation belongs in `codex-rs/core/`; only the indicator belongs in `codex-rs/tui/`.

## MVP Definition

### Launch With (v1)

- [ ] Background automatic mid-turn compaction triggered from the existing token-threshold path in `codex-rs/core/src/codex.rs`
- [ ] Correct replacement of only the compacted transcript prefix/range, preserving messages created after the compaction started
- [ ] Recovery path that stops the agent and falls back to current blocking compaction when background compaction fails
- [ ] TUI indicator below the input showing that compaction is active without adding transcript chatter
- [ ] Regression coverage in `codex-rs/core/tests/suite/compact.rs`, `codex-rs/core/tests/suite/compact_remote.rs`, `codex-rs/app-server/tests/suite/v2/compaction.rs`, and relevant `codex-rs/tui/src/chatwidget/tests.rs`

### Add After Validation (v1.x)

- [ ] True support for multiple concurrent auto-compactions on disjoint transcript ranges once anchoring/splice behavior proves reliable
- [ ] More informative status text such as "compacting history" or count-based summaries if the footer has room
- [ ] Better observability for resume/replay and app-server clients if background compaction state needs richer protocol exposure
- [ ] Performance tuning for local versus remote compaction selection once real-world load patterns are known

### Future Consideration (v2+)

- [ ] Operator diagnostics for compaction efficiency, failure rates, and summary quality
- [ ] Heuristics that pre-compact based on predicted next-turn growth rather than only current usage
- [ ] User-configurable compaction policy knobs, only if there is clear evidence that defaults cannot serve most users

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| Background mid-turn auto-compaction | HIGH | HIGH | P1 |
| Range-safe transcript splicing | HIGH | HIGH | P1 |
| Failure fallback to blocking compaction | HIGH | MEDIUM | P1 |
| TUI footer indicator | HIGH | MEDIUM | P1 |
| Cross-surface event parity | HIGH | MEDIUM | P1 |
| Concurrent compactions on disjoint ranges | MEDIUM | HIGH | P2 |
| Richer progress/detail states | MEDIUM | LOW | P2 |
| Telemetry and diagnostics | MEDIUM | MEDIUM | P2 |
| Predictive/speculative compaction heuristics | LOW | HIGH | P3 |
| User-tunable scheduling controls | LOW | HIGH | P3 |

**Priority key:**
- P1: Must have for launch
- P2: Should have after core correctness is proven
- P3: Defer unless validation shows clear need

## Practical Brownfield Notes

- The safest product boundary is to keep transcript mutation centralized in `codex-rs/core/src/codex.rs` and reuse existing compaction helpers in `codex-rs/core/src/compact.rs` and `codex-rs/core/src/compact_remote.rs`.
- `ContextCompaction` items already exist in `codex-rs/protocol/src/items.rs` and app-server translation already exists in `codex-rs/app-server-protocol/src/protocol/v2.rs`, so the brownfield opportunity is extending lifecycle semantics, not inventing a new public concept.
- The TUI already tracks context usage and footer state in `codex-rs/tui/src/bottom_pane/footer.rs`; that is a better fit for the required indicator than adding more transcript cells in `codex-rs/tui/src/chatwidget.rs`.
- The biggest implementation risk is transcript integrity, not rendering. `codex-rs/core/src/codex.rs`, `codex-rs/core/src/rollout/recorder.rs`, and the persistence concerns called out in `.planning/codebase/CONCERNS.md` make history replacement the review hotspot.
- Existing tests prove the repo already cares about compaction lifecycle and replay semantics. Feature planning should treat `codex-rs/core/tests/suite/compact.rs`, `codex-rs/core/tests/suite/compact_remote.rs`, and `codex-rs/app-server/tests/suite/v2/compaction.rs` as the minimum guardrail set.

## Sources

- `.planning/PROJECT.md`
- `.planning/codebase/ARCHITECTURE.md`
- `.planning/codebase/CONCERNS.md`
- `codex-rs/core/src/codex.rs`
- `codex-rs/core/src/compact.rs`
- `codex-rs/core/src/compact_remote.rs`
- `codex-rs/tui/src/chatwidget.rs`
- `codex-rs/tui/src/bottom_pane/footer.rs`
- `codex-rs/core/tests/suite/compact.rs`
- `codex-rs/core/tests/suite/compact_remote.rs`
- `codex-rs/app-server/tests/suite/v2/compaction.rs`

---
*Feature research for: async rolling auto-compaction in Codex*
*Researched: 2026-03-09*
