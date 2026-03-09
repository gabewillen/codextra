# Phase 3 Research: Durable History And Surface Compatibility

## What Phase 3 Needs To Deliver

Phase 2 made completed background compactions mutate live history safely. Phase 3 needs to make that same transcript shape durable and visible across every surface that reconstructs or reads history later.

To satisfy `HIST-03` and `COMP-03`, this phase must ensure:

- resumed and forked sessions reconstruct the same post-compaction history that live users saw after Phase 2 apply
- rollback replay keeps history, token state, and reference-context baselines aligned after spliced compaction results
- app-server thread/read, thread/resume, thread/rollback, and related thread-item surfaces stay compatible with persisted compaction markers and replacement history
- existing compaction-oriented notifications and thread-item consumers keep their current compatibility contract while the persisted history shape becomes durable

Phase 3 should stay narrower than later work:

- do not add failure fallback for background compaction; that is Phase 4
- do not add below-input visibility or multi-compaction overlap; that is Phase 5
- do not redesign manual `/compact` or pre-turn protective compaction semantics

## Phase 2 Runtime State To Preserve

Phase 2 introduced one critical behavior change:

- `codex-rs/core/src/codex.rs` now calls `replace_compacted_history(...)` with the fully spliced live history, not just the compacted prefix

That means persisted `RolloutItem::Compacted` entries can now carry:

- a `message`
- `replacement_history` that already includes preserved tail items appended after compaction started

This is the durability seam for Phase 3. If replay and read surfaces do not interpret this persisted shape consistently, users will see different histories depending on whether they stay live, resume, fork, rollback, or read through app-server.

## Relevant Core Surfaces

### Resume and fork reconstruction

`codex-rs/core/src/codex.rs` `record_initial_history(...)` already reconstructs history from persisted rollout items for:

- `InitialHistory::Resumed`
- `InitialHistory::Forked`

Both flow through:

- `reconstruct_history_from_rollout(...)`

The existing tests in `codex-rs/core/src/codex_tests.rs` already cover:

- `reconstruct_history_matches_live_compactions`
- `reconstruct_history_uses_replacement_history_verbatim`
- `record_initial_history_reconstructs_resumed_transcript`
- `record_initial_history_reconstructs_forked_transcript`

Planning implication:

- Phase 3 should extend these tests around the new Phase 2 spliced replacement histories, not invent a second replay path
- any replay mismatch is likely a reconstruction or persistence issue, not a live runtime issue

### Rollback replay

`codex-rs/core/src/codex.rs` thread rollback:

1. flushes rollout
2. reloads persisted history
3. appends a synthetic `ThreadRolledBack` event
4. re-runs `reconstruct_history_from_rollout(...)`
5. replaces in-memory history and previous-turn/reference-context state

Existing rollback tests in `codex-rs/core/src/codex_tests.rs` already assert:

- dropped turns
- cumulative rollback markers
- recomputed previous-turn settings
- recomputed reference-context state

Planning implication:

- Phase 3 needs rollback cases that include persisted spliced compactions, not just legacy compaction markers
- the main correctness risk is not the rollback event itself; it is whether replaying a persisted spliced `CompactedItem.replacement_history` yields the same transcript users saw live

### Rollout persistence and listing

Relevant rollout surfaces:

- `codex-rs/core/src/rollout/recorder.rs`
- `codex-rs/core/src/rollout/list.rs`
- `codex-rs/core/src/rollout/metadata.rs`

`RolloutItem::Compacted` is already preserved by recorder and metadata logic. The main Phase 3 question is whether any consumer still assumes compaction items only summarize an entire history prefix without appended tail state.

## Relevant App-Server Surfaces

### Thread history reconstruction

`codex-rs/app-server-protocol/src/protocol/thread_history.rs` converts persisted rollout items into v2 `Turn` / `ThreadItem` structures via:

- `build_turns_from_rollout_items(...)`

Important current behavior:

- `RolloutItem::Compacted(_)` does not emit transcript content directly into turn items
- instead it marks `saw_compaction = true`
- compaction-only turns are preserved so they are not dropped as empty legacy turns

This is intentionally lightweight, but it also means app-server thread-history views do not reconstruct the same transcript content that core replay does from `replacement_history`.

Planning implication:

- Phase 3 likely needs explicit compatibility decisions for thread-history surfaces:
  - either enrich thread-history reconstruction to reflect replacement-history effects
  - or prove current app-server consumers only need compaction markers and are still compatible

Given `COMP-03`, planning should assume at least some targeted thread-history/read assertions are required so this choice is explicit instead of accidental.

### Thread read and resume flows

`codex-rs/app-server/src/codex_message_processor.rs` uses `build_turns_from_rollout_items(...)` for:

- `thread/read`
- rollout-backed thread loading
- resume-turn population

Planning implication:

- a mismatch between core replay and app-server thread-history reconstruction will show up here immediately
- Phase 3 should cover both static read (`thread/read includeTurns`) and resumed state population

### Existing app-server tests

Current app-server coverage in `codex-rs/app-server/tests/suite/v2/compaction.rs` verifies:

- context compaction start/completed notifications
- manual compaction start behavior

It does not yet prove that post-compaction thread history matches the live Phase 2 transcript shape.
It also does not yet prove that existing compaction-oriented notifications and thread-item consumers remain compatible once persisted background compactions replay through the same rollout history.

## Main Risks

### Replay/live mismatch risk

Core live history now persists a spliced `replacement_history`. If replay still behaves like older prefix-only compaction semantics, resumed or forked sessions will diverge from the original live transcript.

### Reference-context drift risk

Phase 2 re-established `reference_context_item` through the spliced apply path. Resume and rollback already rebuild that baseline, but Phase 3 must prove the persisted post-compaction baseline matches what later turns diff against. Otherwise resumed sessions may over-inject or under-inject context updates.

### App-server visibility mismatch risk

App-server `thread/read` and resume turn building currently preserve compaction markers more than full transcript replacement semantics. If this remains unchanged, app-server consumers may show a different thread shape from core resume/live behavior.

### Legacy compaction compatibility risk

Some persisted rollouts may still contain older `CompactedItem` values without `replacement_history`. Phase 3 must preserve backward compatibility while making new spliced compactions durable.

## Recommended Plan Shape

The cleanest Phase 3 breakdown is:

1. lock durable core reconstruction around spliced replacement histories for resume/fork flows
2. align rollback and app-server thread-history/read consumers with the same persisted compaction semantics while preserving existing thread-item and notification compatibility
3. add regression coverage and any minimal docs/schema touchpoints required for the compatible surfaces

That keeps the phase aligned with the two requirements:

- `HIST-03`: durability across live, resume, rollback, read
- `COMP-03`: existing app-server, thread-item, and compaction-notification surfaces remain compatible

## Likely Files To Change

- `codex-rs/core/src/codex.rs`
  - if any replay/reference-context fixes are needed in `record_initial_history(...)` or rollback replay
- `codex-rs/core/src/codex_tests.rs`
  - resumed, forked, and rollback cases for spliced replacement histories
- `codex-rs/core/src/rollout/*`
  - only if a persistence/listing consumer still assumes legacy compaction shape
- `codex-rs/app-server-protocol/src/protocol/thread_history.rs`
  - thread-history reconstruction and compaction compatibility rules
- `codex-rs/app-server/tests/suite/v2/compaction.rs`
  - post-compaction thread read / includeTurns coverage
- `codex-rs/app-server/tests/suite/v2/thread_rollback.rs`
  - rollback coverage when compaction has already rewritten persisted history
- `codex-rs/app-server/README.md`
  - if thread-history/read semantics change materially

## Validation Architecture

### 1. Core replay and rollback parity

Add core tests proving that persisted spliced `CompactedItem.replacement_history` reconstructs to the exact same transcript across:

- resume
- fork
- rollback replay

The best home is `codex-rs/core/src/codex_tests.rs`.

### 2. App-server thread-history compatibility

Add app-server-protocol and app-server tests proving that:

- thread history built from rollout items remains stable when compaction markers and replacement history are present
- `thread/read` and resume-backed turn loading do not regress on compaction-only or rollback flows
- existing compaction notification and thread-item flows remain compatible with the new persisted background compaction shape

The likely homes are:

- `codex-rs/app-server-protocol/src/protocol/thread_history.rs`
- `codex-rs/app-server/tests/suite/v2/compaction.rs`
- `codex-rs/app-server/tests/suite/v2/thread_rollback.rs`

### 3. Legacy compatibility guard

Keep or extend the existing legacy-compaction tests that cover `replacement_history: None`, so new durable behavior does not break older persisted sessions.

## Open Questions

1. Should app-server thread-history surfaces render full post-compaction transcript effects or remain marker-only?
   Recommendation: plan toward whichever option preserves observable compatibility with current clients, but make that choice explicit and tested.

2. Does any rollout-listing or thread-summary code assume compaction items are always prefix-only?
   Recommendation: inspect only the consumers that actually materialize turns or history views; do not broaden Phase 3 into unrelated metadata plumbing unless a concrete mismatch is found.

3. Should docs change in this phase?
   Recommendation: only if app-server thread-history or read semantics become more explicit or change user-visible expectations.

---
*Phase: 03-durable-history-and-surface-compatibility*
*Research completed: 2026-03-09*
