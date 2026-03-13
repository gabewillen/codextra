# Phase 5 Research: Visible Rolling Background Compaction

## What Phase 5 Needs To Deliver

Phase 5 is the overlap and UX phase for async rolling compaction.

To satisfy `RUN-03`, `VIS-01`, and `VIS-02`, this phase must ensure:

- Codex can keep more than one automatic background compaction in flight at the same time
- each overlapping background compaction targets a distinct captured transcript range instead of re-running the same snapshot repeatedly
- successful background compactions remain invisible in the transcript itself
- users can still see that background compaction work is happening, and that signal lives below the input instead of interrupting the transcript
- the visible indicator stays correct when background compactions start, finish, get skipped as stale, or fail into the Phase 4 blocking fallback path

This phase should stay narrower than a general compaction redesign:

- do not change manual `/compact` into a non-blocking operation
- do not weaken Phase 4's rule that failures stop the turn and recover through the blocking path
- do not invent a second durable history model for compaction markers

## Current Core Limitation: Single-Flight Background Compaction

`codex-rs/core/src/state/turn.rs` still models background auto-compaction as exactly one in-flight worker plus exactly one completed result:

- `ActiveTurn.background_auto_compaction: Option<BackgroundAutoCompaction>`
- `ActiveTurn.completed_background_auto_compaction: Option<CompletedBackgroundAutoCompaction>`

The gate is explicit:

- `can_start_background_auto_compaction()` only returns true when both options are `None`
- `set_background_auto_compaction(...)` refuses to start a second worker while one is running or waiting to be applied

Planning implication:

- the current state model cannot satisfy `RUN-03`
- overlapping rolling compaction needs a turn-owned collection keyed by snapshot/job identity, not a single slot

## Where Automatic Background Compactions Start Today

`codex-rs/core/src/codex.rs` starts automatic mid-turn compaction in `start_background_auto_compact(...)` from the post-sampling loop when:

- total token usage crossed the auto-compact limit
- the model reported `needs_follow_up`

Today the start path captures:

- `snapshot_history`
- a generated `ContextCompactionItem` id used as `snapshot_marker`
- a worker handle plus cancellation token

Then it emits `ItemStarted(ContextCompaction)` and launches either:

- `crate::compact::compute_local_compact_result(...)`
- `crate::compact_remote::run_remote_compact_worker(...)`

Planning implication:

- overlap can likely stay in the same orchestration layer because Phase 1 already separated worker execution from later apply
- the main missing pieces are launch eligibility and multi-job bookkeeping

## Distinct-Range Eligibility Is The Real Overlap Constraint

The roadmap says multiple automatic background compactions may overlap on different transcript ranges, not arbitrary duplicate ranges.

That matters because the current trigger runs after follow-up-capable sampling steps. Without an additional eligibility rule, a rolling implementation could:

- launch multiple jobs against the exact same captured prefix
- emit redundant UI activity
- race equivalent completions and failures for no user-visible gain

The cleanest planning assumption is that each background job needs captured-range metadata in addition to `snapshot_marker`, such as:

- captured prefix length
- launch order
- or another monotonic range discriminator derived from the captured transcript

That metadata is needed to answer two questions:

1. Is a newly requested background compaction actually newer than the newest still-relevant captured range?
2. If several jobs finish out of order, which successful completion is still safe to apply first?

## Current Apply And Recovery Paths Assume One Completed Job

Phase 2 and Phase 4 both operate on single completed outcomes:

- `apply_completed_background_auto_compact_if_ready(...)` takes one successful completed job
- `recover_failed_background_auto_compact_if_ready(...)` takes one failed completed job

Both functions currently consume from single-slot helpers in `ActiveTurn`.

That is safe for one background job, but rolling overlap introduces new ordering problems:

- an older successful completion may finish after a newer one
- a newer success may make an older success permanently stale
- a failure in any still-relevant background job must stop the turn exactly once and cancel remaining background work

Planning implication:

- Phase 5 needs deterministic completion draining, not just a `Vec` of finished jobs
- apply/recovery should probably consume according to launch/range order, then drop stale jobs once a newer applied prefix makes them impossible to splice

## Transcript Splicing Still Gives The Safety Boundary

Phase 2 already established the important correctness contract:

- only rewrite live history when `ContextManager::splice_compacted_prefix(...)` can prove the live prefix still matches the captured snapshot
- otherwise skip the completed background compaction

That same contract should remain the safety boundary for overlapping jobs.

Planning implication:

- rolling overlap does not need a new splicing algorithm
- it does need queue/ordering logic around the existing splice helper so stale completions are skipped deliberately instead of accidentally winning races

## Failure Recovery Gets Harder With Multiple Jobs

Phase 4 added single-job failure recovery:

- a failed background job becomes a completed failed outcome
- the active turn consumes that failure exactly once
- the turn stops and runs the existing blocking compaction path

With multiple background jobs, Phase 5 must keep the same user contract while avoiding double resolution:

- the first relevant failure should trigger the blocking fallback once
- other in-flight background jobs should be cancelled or ignored without emitting extra terminal behavior
- successful completions that arrive after failure recovery begins must not rewrite history behind the fallback path

Planning implication:

- multi-job cancellation and terminal-outcome ownership belong in the same turn-owned state machine as rolling overlap
- Phase 5 cannot treat concurrency as a launch-only refactor

## Current TUI Surface Does Not Match The Requested UX

The current busy UI lives in `codex-rs/tui/src/status_indicator_widget.rs` and is rendered from `BottomPane` above the composer, not below it.

`codex-rs/tui/src/bottom_pane/mod.rs` currently renders:

- optional status row above the composer
- pending-input previews above the composer
- the composer itself

`codex-rs/tui/src/bottom_pane/chat_composer.rs` already owns footer rows that render beneath the input.

Planning implication:

- the requested compaction indicator should likely be a composer/footer-level surface, not another use of the generic task-running status row
- this avoids conflating "agent is busy" with "background compaction is active"

## Successful Background Compaction Still Produces Transcript Chatter

The current legacy compaction surface is still visible in transcript-oriented clients:

- `ContextCompactionItem::as_legacy_event()` maps to `EventMsg::ContextCompacted(...)`
- `codex-rs/tui/src/chatwidget.rs` renders that legacy event as the agent message `"Context compacted"`
- current core tests for manual local and remote compaction explicitly assert that legacy event

That behavior violates `VIS-02` for successful background compactions.

Planning implication:

- the new below-input indicator should be driven by compaction lifecycle events, not by transcript messages
- successful automatic background compactions need a deliberate suppression path for transcript chatter
- if legacy compaction semantics change, manual compaction behavior and deprecated app-server docs must be updated deliberately rather than incidentally

## Existing Protocol Surfaces Are Close, But Not Perfect

App-server already exposes:

- `item/started` for `ThreadItem::ContextCompaction { id }`
- `item/completed` for the same item id on successful completion
- durable thread history marker items for persisted compaction checkpoints

That is enough to drive a lightweight activity indicator for successful jobs.

The gap is failure accuracy:

- failed background jobs currently do not emit `item/completed`
- `ThreadItem::ContextCompaction` carries only an id, so durable history does not encode success vs failure

Planning implication:

- Phase 5 should avoid polluting durable thread history with failed background markers just to drive the footer indicator
- if indicator accuracy on failure needs extra signaling, prefer a transient surface or carefully scoped lifecycle rule over widening durable thread items casually

## Existing Tests To Build On

Core already has strong compaction coverage in:

- `codex-rs/core/tests/suite/compact.rs`
- `codex-rs/core/tests/suite/compact_remote.rs`

Important starting points:

- `multiple_auto_compact_per_task_runs_after_token_limit_hit` currently proves only one background compaction starts
- same-turn apply coverage already proves immediate continuation happens before successful background apply
- Phase 4 tests already prove failed background compaction falls back to blocking compaction once

TUI already has the right coverage style in:

- `codex-rs/tui/src/chatwidget/tests.rs`
- existing `insta` snapshots for bottom-pane and footer rendering

App-server already has compaction compatibility suites in:

- `codex-rs/app-server/tests/suite/v2/compaction.rs`

The missing Phase 5 coverage is:

- multiple automatic background compactions can overlap on distinct captured ranges
- multiple successful completions preserve same-turn tail ordering after rolling apply
- one failure among overlapping background jobs triggers a single fallback and clears remaining background activity
- the below-input indicator appears while background compaction is active and stays out of the transcript
- successful background compaction no longer produces visible transcript chatter in the TUI

## Main Risks

### Duplicate-range launch risk

If Phase 5 only replaces the single slot with a list, Codex can start redundant background jobs against the same snapshot and waste both UI and model budget.

### Out-of-order apply risk

If successful completions are applied in completion order instead of captured-range order, an older result can win a race and either fail to splice or overwrite a more useful newer compaction.

### Failure-fanout risk

If one overlapping background job fails while others are still running, fallback could trigger more than once or later successful completions could continue mutating history after recovery starts.

### Surface drift risk

If the TUI indicator is built on the generic status row above the composer, the feature will miss the user-requested placement and keep agent-busy state mixed together with compaction state.

### Compatibility risk

If successful auto-compaction chatter is removed by changing the deprecated legacy event surface, manual compaction and app-server README examples must be updated intentionally.

## Recommended Plan Shape

The cleanest Phase 5 breakdown is:

1. refactor core background compaction state from a single slot into rolling tracked jobs with explicit distinct-range eligibility
2. make overlap execution deterministic by ordering apply, stale-drop, cancellation, and failure fallback behavior across many jobs
3. add a dedicated below-input compaction indicator and stop successful background compactions from writing transcript chatter
4. lock the phase behind core, TUI, and app-server regressions so rolling overlap and UI behavior stay aligned

That keeps the phase aligned with the roadmap:

- concurrency first
- safe rolling behavior second
- visible UX third
- compatibility proof last

## Likely Files To Change

- `codex-rs/core/src/state/turn.rs`
- `codex-rs/core/src/codex.rs`
- `codex-rs/core/src/tasks/mod.rs`
- `codex-rs/core/tests/suite/compact.rs`
- `codex-rs/core/tests/suite/compact_remote.rs`
- `codex-rs/tui/src/chatwidget.rs`
- `codex-rs/tui/src/chatwidget/tests.rs`
- `codex-rs/tui/src/bottom_pane/mod.rs`
- `codex-rs/tui/src/bottom_pane/chat_composer.rs`
- `codex-rs/tui/src/bottom_pane/footer.rs`
- `codex-rs/app-server/tests/suite/v2/compaction.rs`
- `codex-rs/app-server/README.md` if deprecated compaction notification semantics or UI guidance change

## Validation Architecture

### 1. Rolling core state and apply ordering

Add targeted core tests proving that:

- multiple background compactions can be in flight at once on distinct captured ranges
- later same-turn continuation requests still proceed before any later compaction apply mutates history
- overlapping successful completions either apply in the intended order or are dropped as stale without corrupting the tail

The likely homes are:

- `codex-rs/core/tests/suite/compact.rs`
- `codex-rs/core/tests/suite/compact_remote.rs`

### 2. Failure and cancellation behavior under overlap

Keep or extend explicit regressions for:

- one failed overlapping background job triggering the blocking fallback exactly once
- remaining background jobs being cancelled or ignored after failure recovery starts
- aborted jobs staying aborted instead of being reclassified as failures

The likely homes are:

- `codex-rs/core/tests/suite/compact.rs`
- `codex-rs/core/tests/suite/compact_remote.rs`
- `codex-rs/core/src/codex_tests.rs`

### 3. Footer indicator and no-chatter UX

Add TUI tests and snapshots proving that:

- a compaction indicator renders below the input while background compaction is active
- the indicator stays accurate as compactions start and settle
- successful background compaction no longer inserts `"Context compacted"` transcript chatter

The likely homes are:

- `codex-rs/tui/src/chatwidget/tests.rs`
- `codex-rs/tui/src/snapshots/*compaction*`

### 4. App-server compatibility

Extend app-server compaction coverage so that:

- overlapping background compactions still emit the expected item lifecycle notifications
- durable history/read/resume behavior remains compatible with the marker-only compaction contract
- any deprecated compaction notification behavior that changes is reflected in `codex-rs/app-server/README.md`
