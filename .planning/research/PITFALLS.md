# Pitfalls Research

**Domain:** Brownfield background auto-compaction for long-running Codex threads
**Researched:** 2026-03-09
**Confidence:** HIGH

## Critical Pitfalls

### Pitfall 1: Applying a compacted summary to the wrong transcript slice

**What goes wrong:**
Background compaction finishes after new messages, tool outputs, or even another compaction have already been appended. If the completion path still calls whole-history replacement logic like `replace_compacted_history` in `codex-rs/core/src/codex.rs`, it can drop fresh items, duplicate older ones, or move the latest summary above the wrong user message.

**Why it happens:**
The current compaction flow is inline and single-owner. `run_auto_compact` in `codex-rs/core/src/codex.rs`, `run_inline_auto_compact_task` in `codex-rs/core/src/compact.rs`, and `run_inline_remote_auto_compact_task` in `codex-rs/core/src/compact_remote.rs` all assume the history they compact is still the history they are allowed to replace.

**How to avoid:**
Capture an explicit source range or transcript generation when background compaction starts, and only splice if that exact prefix is still present. Treat completion as a compare-and-swap on a known cutoff, not as blind `Vec<ResponseItem>` replacement. Persist the same splice metadata into the rollout checkpoint so resume/fork/rollback can replay it deterministically.

**Warning signs:**
- Post-compaction follow-up requests stop containing the newest user text.
- A second compaction output appears above an older summary instead of above the latest live turn.
- Resume or fork tests start losing `USER_TWO`-style messages that arrived during compaction.

**Phase to address:**
Phase 1: Core background compaction state machine and splice model.

---

### Pitfall 2: Rollout JSONL and SQLite metadata drift during async compaction

**What goes wrong:**
The in-memory thread view looks correct, but `thread/read`, `thread/resume`, thread list, or rollback reload stale or inconsistent state because the append-only rollout and SQLite metadata were not updated in the same durable order. The result is replaying an older transcript, stale previews, or a thread that looks compacted in memory but not on disk.

**Why it happens:**
`codex-rs/core/src/rollout/recorder.rs` writes through a background channel, while `codex-rs/state/src/runtime/threads.rs` updates SQLite separately. `codex-rs/app-server/src/codex_message_processor.rs` still reads rollout files directly for summaries and turns, and `RolloutRecorder::list_threads_with_db_fallback` explicitly repairs DB drift from filesystem state.

**How to avoid:**
Keep compaction persistence append-only and ordered: write the compaction checkpoint, flush it when completion must be externally observable, then update metadata. Never mutate the rollout file in place. Add a durable compaction marker that includes the splice boundary so `codex-rs/core/src/codex/rollout_reconstruction.rs` and `codex-rs/app-server/src/codex_message_processor.rs` can agree on the newest valid state.

**Warning signs:**
- `thread/list` and `thread/read` disagree on preview or `updated_at`.
- Rollout replay logs parse errors or reports “does not start with session metadata”.
- Rollback works in-memory but a later resume reloads the removed turn.

**Phase to address:**
Phase 2: Persistence, replay, and recovery semantics.

---

### Pitfall 3: Background failure creates split-brain recovery behavior

**What goes wrong:**
A background compaction fails, but the agent either keeps running on an over-limit transcript or both the background completion path and the blocking fallback try to “fix” history. That creates double-compaction, duplicate completion items, or a turn that looks alive in the UI while the core has already decided to stop it.

**Why it happens:**
Current failures are synchronous and decisive. For example, `auto_remote_compact_failure_stops_agent_loop` in `codex-rs/core/tests/suite/compact_remote.rs` proves the inline flow halts the turn. A background feature changes that control flow and must reintroduce a single authoritative owner for “continue, stop, or fallback”.

**How to avoid:**
Model background compaction as a strict state machine per turn: `Pending`, `Applied`, `Failed`, `FallbackRunning`, `FallbackApplied`, `Aborted`. Failure must atomically block further model progress for the affected turn before starting the old interrupting path. Completion handlers must no-op if fallback already won.

**Warning signs:**
- Tool calls continue after a compaction error that should have stopped the turn.
- The same compaction id emits both success and fallback events.
- The status indicator never clears after a failed background compact.

**Phase to address:**
Phase 1: Core background compaction state machine and fallback arbitration.

---

### Pitfall 4: Replay, fork, and rollback stop matching the live transcript

**What goes wrong:**
The live conversation seems correct, but `resume`, `fork`, or rollback rebuilds the wrong history because replay chooses the wrong compaction checkpoint or drops the wrong turn boundary. This is especially risky once multiple background compactions can finish out of order on different ranges.

**Why it happens:**
`codex-rs/core/src/codex/rollout_reconstruction.rs` intentionally reconstructs from the newest surviving `RolloutItem::Compacted` plus trailing rollout items. That logic works for current append-only checkpoints, but it assumes a simple total order and a clear “newest surviving replacement history” concept.

**How to avoid:**
Make background compaction checkpoints explicitly replayable. Each persisted compaction record should say which transcript prefix it replaces and which generation it supersedes. Extend `codex-rs/core/src/codex/rollout_reconstruction_tests.rs`, `codex-rs/core/tests/suite/compact_resume_fork.rs`, and `codex-rs/app-server/tests/suite/v2/thread_rollback.rs` with interleavings where compactions finish after newer user/tool items and where rollback crosses multiple checkpoints.

**Warning signs:**
- Resume shows an older summary after a newer one already applied live.
- Rollback removes the wrong post-compaction turn.
- Forked threads rebuild context from a superseded compacted prefix.

**Phase to address:**
Phase 2: Persistence, replay, and recovery semantics.

---

### Pitfall 5: Reusing the existing TUI running indicator without a compaction-specific model

**What goes wrong:**
The required “indicator below the input” flickers, disappears during stream output, overwrites the main “Working” state, or competes with the background terminal footer. Users either miss that compaction is running or think the agent itself is stalled.

**Why it happens:**
`codex-rs/tui/src/chatwidget.rs`, `codex-rs/tui/src/bottom_pane/mod.rs`, and `codex-rs/tui/src/status_indicator_widget.rs` currently expose one task-running indicator with header swapping and stream-driven hide/restore behavior. That system already handles commentary, retries, undo, and background terminal waiting; adding compaction as just another header string is likely to create collisions.

**How to avoid:**
Track compaction separately from the main task-running surface. Either merge it as a secondary detail/count within the status row, or render a dedicated footer/status substate that does not steal interrupt hints or current task headers. Test narrow widths, queued messages, commentary streaming, and unified-exec wait states before settling on the final layout.

**Warning signs:**
- “Compacting…” replaces “Waiting for background terminal” or normal working text.
- The indicator vanishes as soon as streamed output resumes.
- Snapshot diffs show queue hints, context text, or collaboration mode labels being pushed off-screen.

**Phase to address:**
Phase 3: TUI and client visibility behavior.

---

### Pitfall 6: Background compaction storms on long tool-heavy turns

**What goes wrong:**
Once the transcript crosses the token threshold, every additional tool result or model continuation tries to schedule another compaction. The session wastes cycles compacting overlapping ranges, remote compaction requests pile up, and rollout files accumulate excessive checkpoints.

**Why it happens:**
The current mid-turn logic in `codex-rs/core/src/codex.rs` checks token usage inline after each sampling step. That is acceptable for one blocking compaction, but background mode changes the cost model and allows multiple in-flight jobs.

**How to avoid:**
Debounce scheduling by transcript generation and threshold band. Allow only one compaction per replaceable prefix, and require a minimum amount of new transcript growth before spawning the next job. For remote compaction, cap concurrency and prefer newest-useful work over stale pending work.

**Warning signs:**
- Multiple compaction requests are launched without enough new messages to justify them.
- Long shell-heavy turns produce many tiny `RolloutItem::Compacted` entries.
- Token usage drops only slightly after compaction, then immediately triggers another compact.

**Phase to address:**
Phase 1: Core scheduling and concurrency limits.

---

### Pitfall 7: Tests stay green while the real race matrix is still untested

**What goes wrong:**
The feature appears complete because existing compaction, app-server, and TUI tests still pass, but the shipped system has holes around delayed completion, out-of-order finish, crash recovery, and cross-client visibility.

**Why it happens:**
The current suites in `codex-rs/core/tests/suite/compact.rs`, `codex-rs/core/tests/suite/compact_remote.rs`, and `codex-rs/app-server/tests/suite/v2/compaction.rs` mostly exercise inline behavior: started/completed item emission, prompt shape, and failure of the current blocking flow. They do not cover concurrent background compactions, restart after compaction persisted but before UI delivery, or app-server/TUI observing completion from persisted state.

**How to avoid:**
Add deterministic harnesses that delay compaction completion until after more transcript growth, then assert live state, persisted rollout, DB metadata, and replayed history all converge. Cover local and remote compaction, success and failure, out-of-order completion, and restart-after-crash.

**Warning signs:**
- The only new assertions are item start/completed notifications.
- No test forces compaction to complete after later user/tool messages already landed.
- No restart test reloads from a rollout containing multiple compaction checkpoints.

**Phase to address:**
Phase 4: Race, replay, and UI verification matrix.

## Technical Debt Patterns

Shortcuts that look attractive for v1 but create persistent correctness debt.

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Reuse `replace_compacted_history` unchanged for background completions | Smallest core diff | Drops or reorders live items when completions race | Never |
| Key compaction ordering only by finish time | Easy to reason about in logs | Older work can overwrite newer transcript state | Never |
| Mutate existing rollout JSONL sections in place | Avoids extra replay logic | High corruption risk; breaks append-only recovery assumptions in `codex-rs/core/src/rollout/recorder.rs` | Never |
| Treat SQLite as optional best-effort metadata only | Faster first ship | `thread/list`, `thread/read`, and resume drift grows under failure | Only during bring-up behind a feature flag, not for release |
| Use the current status header as the only compaction indicator | Minimal TUI code | Header conflicts with retries, commentary, undo, and unified exec | Prototype only |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|------------------|
| Core compaction engine + session history (`codex-rs/core/src/codex.rs`, `codex-rs/core/src/compact.rs`, `codex-rs/core/src/compact_remote.rs`) | Applying compaction results as if history were still static | Splice against a captured prefix/generation and reject stale completions |
| Rollout writer + SQLite (`codex-rs/core/src/rollout/recorder.rs`, `codex-rs/state/src/runtime/threads.rs`) | Updating memory state first and persisting later | Persist append-only checkpoint first, then reconcile metadata with the same boundary |
| Replay + rollback (`codex-rs/core/src/codex/rollout_reconstruction.rs`, `codex-rs/app-server/tests/suite/v2/thread_rollback.rs`) | Assuming “latest compaction wins” without source-range metadata | Persist enough information to prove which prefix each checkpoint replaces |
| Core events + TUI status surface (`codex-rs/tui/src/chatwidget.rs`, `codex-rs/tui/src/bottom_pane/mod.rs`) | Mapping compaction to generic background text updates | Give compaction its own visible state that coexists with the main running indicator |
| App-server compaction notifications (`codex-rs/app-server/tests/suite/v2/compaction.rs`) | Verifying only started/completed notifications | Also verify persisted transcript shape and resumed thread shape after those notifications |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| Scheduling a new compact on every threshold crossing | Repeated compact requests, little token drop, noisy rollouts | Debounce by transcript generation and minimum new-token delta | Tool-heavy turns with repeated follow-up sampling |
| Flushing rollout writes too aggressively | High I/O churn and UI-visible latency spikes | Flush only when external observers require durability, not for every intermediate marker | Long sessions with many event items |
| Replay scanning many superseded checkpoints | Resume/fork/rollback gets slower as the rollout grows | Keep checkpoints replayable and short-circuitable; prefer explicit supersession metadata | Threads with many compactions or crash-recovery retries |
| TUI redraw churn from spinner plus compaction indicator changes | Flicker, dropped input responsiveness, unstable snapshots | Reuse existing frame scheduling rules and avoid emitting high-frequency compaction progress text | Narrow terminals or busy multi-event sessions |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Trusting remote compact output to preserve instruction boundaries | Old developer/session wrapper content can leak back into the live prompt | Keep filtering rules like `should_keep_compacted_history_item` in `codex-rs/core/src/compact_remote.rs` and apply the same rules in the background path |
| Accepting a stale compaction result for the wrong transcript generation | Transcript integrity failure looks like data corruption, not a normal bug | Bind each compaction result to a specific thread id, turn id, and source prefix/generation |
| Silently replaying partially corrupted rollout files | Resume/fork can reconstruct misleading history after parse loss | Surface parse-error telemetry, add repair/fallback paths, and fail closed when the newest compaction checkpoint is unreadable |

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|-------------|-----------------|
| No visible background state while compaction is running | Users think the model is idle or stuck | Show a lightweight persistent indicator below the composer while any compaction is in flight |
| Indicator steals the main working state | Users cannot tell whether the agent is still acting or only compacting | Keep compaction as a secondary status, not a replacement for the active task label |
| Success rewrites transcript with no continuity cue | Users perceive “missing” conversation chunks | Preserve post-start messages below the new compacted top message and keep ordering stable |
| Failure falls back silently to blocking behavior | The UI suddenly pauses with no explanation | Announce fallback explicitly and keep the indicator/state consistent until recovery finishes |
| Multiple in-flight compactions are shown as a single ambiguous spinner | Users cannot tell whether the system is making progress or stuck | Show count or state that distinguishes one running compact from several queued/running compactions |

## "Looks Done But Isn't" Checklist

- [ ] **Core splice logic:** Background completion is generation-checked, not blind whole-history replacement.
- [ ] **Rollout durability:** The persisted `RolloutItem::Compacted` data is sufficient for `resume`, `fork`, and rollback to rebuild the same transcript.
- [ ] **Fallback path:** A failed background compact provably stops or gates further model progress before the blocking recovery path starts.
- [ ] **App-server parity:** `thread/read`, `thread/resume`, and rollback match the live transcript after compaction success and after fallback.
- [ ] **TUI visibility:** The indicator survives streaming, queued messages, narrow widths, and unified-exec states without hiding the main task state.
- [ ] **Race coverage:** Tests include delayed completion, out-of-order completion, restart after persisted checkpoint, and multiple overlapping compactions on different ranges.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| Wrong-slice splice applied | HIGH | Stop the turn, append an explicit recovery checkpoint, rebuild from rollout via `codex-rs/core/src/codex/rollout_reconstruction.rs`, then re-run blocking compaction on the repaired history |
| Rollout/SQLite drift | MEDIUM | Flush rollout, reconcile metadata from the rollout path, and compare `thread/read` output against replayed history before resuming work |
| Background failure split-brain | HIGH | Cancel or pause the affected turn, mark the background job terminal, run the legacy blocking compaction path, then emit one final visible state transition |
| Replay/rollback mismatch | HIGH | Reload from persisted rollout, validate newest surviving compaction checkpoint, and discard stale checkpoints that do not match the latest known prefix |
| TUI indicator conflict | LOW | Keep the transcript untouched, rebuild bottom-pane state from current task + compaction counters, and rely on snapshot/VT100 regression tests before shipping |
| Compaction storm | MEDIUM | Drop stale queued compactions, keep the newest useful one, and raise the debounce threshold before reenabling background scheduling |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| Wrong transcript slice replacement | Phase 1: Core background compaction state machine | Deterministic test where compaction starts, more items arrive, then completion preserves the newer tail |
| Rollout/SQLite drift | Phase 2: Persistence, replay, and recovery semantics | Restart the process and prove `thread/read`, `resume`, and live history agree |
| Split-brain failure/fallback | Phase 1: Core background compaction state machine | Failure test proves the agent stops or gates progress before fallback and only one recovery path applies |
| Replay/fork/rollback mismatch | Phase 2: Persistence, replay, and recovery semantics | Extend `compact_resume_fork` and rollback tests with multiple persisted checkpoints |
| TUI indicator conflicts | Phase 3: TUI and client visibility behavior | Snapshot and VT100 coverage for busy, narrow, queued-message, and unified-exec layouts |
| Compaction storms | Phase 1: Core scheduling and concurrency limits | Long-turn test proves only useful compactions are scheduled |
| Missing race matrix | Phase 4: Race, replay, and UI verification matrix | Cross-crate tests cover local/remote success, failure, out-of-order completion, and restart |

## Sources

- `./.planning/PROJECT.md`
- `./.planning/codebase/CONCERNS.md`
- `./.planning/codebase/TESTING.md`
- `codex-rs/core/src/codex.rs`
- `codex-rs/core/src/compact.rs`
- `codex-rs/core/src/compact_remote.rs`
- `codex-rs/core/src/rollout/recorder.rs`
- `codex-rs/core/src/codex/rollout_reconstruction.rs`
- `codex-rs/core/tests/suite/compact.rs`
- `codex-rs/core/tests/suite/compact_remote.rs`
- `codex-rs/core/tests/suite/compact_resume_fork.rs`
- `codex-rs/core/tests/suite/sqlite_state.rs`
- `codex-rs/state/src/runtime/threads.rs`
- `codex-rs/app-server/src/codex_message_processor.rs`
- `codex-rs/app-server/tests/suite/v2/compaction.rs`
- `codex-rs/app-server/tests/suite/v2/thread_rollback.rs`
- `codex-rs/tui/src/chatwidget.rs`
- `codex-rs/tui/src/bottom_pane/mod.rs`
- `codex-rs/tui/src/status_indicator_widget.rs`

---
*Pitfalls research for: background auto-compaction in Codex*
*Researched: 2026-03-09*
