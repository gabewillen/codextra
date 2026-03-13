---
phase: 1
slug: background-trigger-and-continued-turns
status: ready
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-09
---

# Phase 1 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Rust `cargo test` workspace suites |
| **Config file** | `codex-rs/Cargo.toml` |
| **Quick run command** | `cargo test -p codex-core compact` |
| **Scoped lint/fix command** | `just fix -p codex-core` for Plans 01-03, then `just fix -p codex-app-server` if Plan 04 changes app-server code materially |
| **Full suite command** | `cargo test` from `codex-rs` after asking the user for approval to run the complete suite, because Phase 1 changes `codex-core` |
| **Estimated runtime** | ~180 seconds for scoped checks; workspace `cargo test` is slower and user-gated |

---

## Sampling Rate

- **After every task commit:** Run the narrowest relevant project test (`cargo test -p codex-core compact`, `cargo test -p codex-core compact_remote`, `cargo test -p codex-core user_shell_cmd`, or `cargo test -p codex-app-server compaction`)
- **Before finalizing a large codex-rs change:** Run `just fix -p codex-core` for Plans 01-03; if Plan 04 materially changes app-server code, also run `just fix -p codex-app-server`
- **After every plan wave:** Run the relevant project-specific suites for the files changed in that wave; do not substitute this for the true complete suite
- **Before `$gsd-verify-work`:** Ask the user before running workspace `cargo test`, then require it to be green because Phase 1 changes `codex-core`
- **Max feedback latency:** 180 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 1-01-01 | 01 | 1 | RUN-01 | integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 1-01-02 | 01 | 1 | RUN-01 | lint/regression | `just fix -p codex-core` | ✅ | ⬜ pending |
| 1-02-01 | 02 | 1 | RUN-01 | integration | `cargo test -p codex-core compact_remote` | ✅ | ⬜ pending |
| 1-02-02 | 02 | 1 | RUN-01 | lint/regression | `just fix -p codex-core` | ✅ | ⬜ pending |
| 1-03-01 | 03 | 2 | RUN-01 | integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 1-03-02 | 03 | 2 | RUN-02 | state/regression | `cargo test -p codex-core user_shell_cmd` | ✅ | ⬜ pending |
| 1-03-03 | 03 | 2 | RUN-01,RUN-02 | lint/regression | `just fix -p codex-core` | ✅ | ⬜ pending |
| 1-04-01 | 04 | 3 | RUN-02 | integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 1-04-02 | 04 | 3 | RUN-02 | regression | `cargo test -p codex-core user_shell_cmd` | ✅ | ⬜ pending |
| 1-04-03 | 04 | 3 | RUN-02 | protocol/regression | `cargo test -p codex-app-server compaction` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `codex-rs/core/tests/suite/compact.rs` — add delayed background-compaction coverage for `RUN-01`
- [ ] `codex-rs/core/tests/suite/compact_remote.rs` — add delayed remote background-compaction coverage for `RUN-01`
- [ ] `codex-rs/core/tests/suite/user_shell_cmd.rs` or equivalent active-turn invariant coverage — prove background auto-compaction does not replace the active turn and is cancelled on turn teardown for `RUN-02`
- [ ] `codex-rs/app-server/tests/suite/v2/compaction.rs` — cover event ordering and no-interrupt regression for `RUN-02`

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Long-running TUI session continues visibly while auto-compaction is active | RUN-02 | The eventual transcript/UI feel is easiest to judge interactively | Run a long tool-heavy session in the TUI, trigger auto-compaction, and confirm the agent keeps streaming output while compaction is in flight |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all required runtime, invariant, and app-server references for Phase 1
- [x] No watch-mode flags
- [x] Feedback latency < 180s for scoped checks
- [x] `nyquist_compliant: true` set in frontmatter
- [x] Complete-suite guidance matches repo rules: project-specific tests run automatically, workspace `cargo test` is user-gated

**Approval:** ready for execution planning
