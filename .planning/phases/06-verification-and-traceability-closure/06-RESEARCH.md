# Phase 6 Research: Verification And Traceability Closure

## What Phase 6 Needs To Deliver

Phase 6 is a milestone closeout phase, not a product phase. The shipped async rolling compaction behavior already exists across core, TUI, and app-server. The audit gap is procedural:

- every completed v1 phase is missing `VERIFICATION.md`
- Phase 2 validation remains partial because `wave_0_complete: false`
- Phase 4 validation remains partial because the file is still `status: draft`
- `REQUIREMENTS.md` still reflects pre-closeout traceability instead of verified milestone completion

Planning should keep the scope narrow:

- do not reopen runtime, TUI, or app-server behavior
- do not redesign requirements or add new v1 scope
- do not treat the audit's full-workspace test note as a blocker unless fresh evidence proves it blocks milestone closure

## Existing Evidence Already Available

The required product evidence already exists in the phase summaries and validation files:

- Phase 1 summaries show background launch without active-turn interruption and scoped `core` plus `app-server` verification for `RUN-01` and `RUN-02`
- Phase 2 summaries show safe prefix splice behavior and preserved tail ordering for `HIST-01` and `HIST-02`
- Phase 3 summaries show durable replay, read, resume, and rollback compatibility for `HIST-03` and `COMP-03`
- Phase 4 summaries show failure fallback plus blocking guardrails for `RECV-01`, `RECV-02`, `COMP-01`, and `COMP-02`
- Phase 5 summaries show rolling overlap plus the below-input indicator and no-chatter contract for `RUN-03`, `VIS-01`, and `VIS-02`

The milestone audit already states there are no product integration gaps and no broken flows. That means Phase 6 should focus on turning existing evidence into formal workflow artifacts instead of searching for new runtime proof.

## Main Closure Seams

### 1. Backfill phase verification reports

Each completed phase needs its own `XX-VERIFICATION.md` with:

- the roadmap goal for that phase
- must-have truths derived from that goal
- requirement coverage mapped to the phase's REQ IDs
- evidence pulled from summary frontmatter, scoped test runs, and any relevant docs/tests
- a `status: passed` result when the evidence is sufficient

This is the largest scope item because it spans five phases, but it is still artifact work, not code work.

### 2. Fix the two partial validation artifacts

The audit's Nyquist gaps are narrow:

- Phase 2 is partial only because `wave_0_complete: false`
- Phase 4 is partial only because the frontmatter `status` is still `draft`

Before changing those files, execution should confirm the bodies still match the already-completed plans and their scoped validation commands. The safest shape is to treat this as validation artifact normalization, not test strategy redesign.

### 3. Close the requirements traceability loop

The audit matrix treats a requirement as fully satisfied only when three sources line up:

- `VERIFICATION.md` status is `passed`
- phase summary frontmatter lists the requirement in `requirements-completed`
- `REQUIREMENTS.md` checkbox is checked

That means Phase 6 must update `REQUIREMENTS.md` after phase verification reports exist. Planning should avoid checking boxes earlier because the audit would still classify them as partial without the verification reports.

### 4. Re-run the milestone audit as the terminal proof

The milestone should only be considered closed when a fresh audit upgrades the status from `gaps_found` to a clean pass state. This should happen after:

- all five `VERIFICATION.md` files exist
- the Phase 2 and Phase 4 validation artifacts are no longer partial
- `REQUIREMENTS.md` reflects satisfied v1 requirements

## Recommended Implementation Shape

### Plan 1: Phase verification backfill for the earlier runtime/history phases

Backfill verification for Phases 1 through 3 first. These phases establish the non-blocking runtime, safe splice behavior, and durable compatibility chain. Grouping them together keeps the evidence review focused on the core behavior ladder and lets later closure work depend on formal verification being present.

### Plan 2: Phase verification backfill for the later recovery/UX phases plus validation debt cleanup

Backfill verification for Phases 4 and 5, then normalize the two partial validation files. These artifacts are adjacent in the audit and can share the same review pass because they both lean on already-green scoped suites and final summary evidence.

### Plan 3: Requirements closeout and final audit rerun

After all verification artifacts exist and validation debt is closed, update `REQUIREMENTS.md` to checked/satisfied state and rerun the milestone audit. This last plan should own milestone closure state updates so the earlier plans stay focused on evidence generation instead of toggling global status too early.

## Likely Files To Change

- `.planning/phases/01-background-trigger-and-continued-turns/01-VERIFICATION.md`
- `.planning/phases/02-safe-transcript-splicing/02-VERIFICATION.md`
- `.planning/phases/03-durable-history-and-surface-compatibility/03-VERIFICATION.md`
- `.planning/phases/04-failure-recovery-and-blocking-guardrails/04-VERIFICATION.md`
- `.planning/phases/05-visible-rolling-background-compaction/05-VERIFICATION.md`
- `.planning/phases/02-safe-transcript-splicing/02-VALIDATION.md`
- `.planning/phases/04-failure-recovery-and-blocking-guardrails/04-VALIDATION.md`
- `.planning/REQUIREMENTS.md`
- `.planning/STATE.md`
- `.planning/ROADMAP.md`
- `.planning/v1.0-MILESTONE-AUDIT.md`

## Main Risks

### Evidence inflation risk

The closeout work could overstate certainty if verification reports merely restate summaries. Plans should explicitly cite the scoped tests, files, and docs that support each requirement instead of copying summary prose.

### Scope creep risk

Because the audit mentions that no full workspace `cargo test` was run, it would be easy to expand Phase 6 into fresh product verification. The audit did not classify that as a requirement or flow gap, so Phase 6 should not turn into a broad retest or code change phase unless the new verification work exposes a real blocker.

### Traceability ordering risk

If `REQUIREMENTS.md` is updated before verification artifacts are in place, the audit matrix can still classify requirements as partial. The plans should enforce the order: verification first, traceability update second, audit rerun last.

## Test Approach

This phase is mostly artifact verification. The useful automated checks are:

- file existence and frontmatter sanity for all five `VERIFICATION.md` reports
- validation frontmatter checks for Phase 2 and Phase 4
- requirement checkbox and traceability consistency checks in `REQUIREMENTS.md`
- rerunning the milestone audit and confirming the new status is no longer `gaps_found`

No new product tests should be required unless verification uncovers a contradiction in the existing evidence.

## Validation Architecture

Validation should use three layers.

### 1. Artifact existence and status checks

Use shell checks to verify all required `VERIFICATION.md` files exist, carry a non-gap status, and cover the expected phase numbers.

### 2. Traceability consistency checks

Validate that every v1 requirement listed in `REQUIREMENTS.md` is checked only after the corresponding verification report passes and that the traceability table no longer points at a pending closeout state.

### 3. Final milestone audit gate

Treat the rerun milestone audit as the top-level verification gate. Phase 6 is only complete when the audit passes without requirement, integration, or flow gaps.

## Open Questions

1. Should Phase 6 rerun any scoped crate tests while writing the verification reports, or rely entirely on the existing recorded evidence?
   Recommendation: rely on the recorded evidence unless a verification report finds conflicting or missing proof.

2. Should the final audit file overwrite the current `v1.0-MILESTONE-AUDIT.md` or produce a new timestamped closeout artifact?
   Recommendation: follow the existing audit workflow's default output so downstream commands keep finding the latest report consistently.

3. Should the closure phase mark itself complete before or after running `$gsd-audit-milestone`?
   Recommendation: after the rerun audit succeeds, because that command is one of the explicit success criteria.
