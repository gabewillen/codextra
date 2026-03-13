---
phase: 5
slug: visible-rolling-background-compaction
status: ready
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-09
---

# Phase 5 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Rust `cargo test` integration + unit tests + TUI snapshot tests |
| **Config file** | `codex-rs/Cargo.toml` |
| **Quick run command** | `cargo test -p codex-core compact && cargo test -p codex-app-server compaction` |
| **Scoped lint/fix command** | `just fix -p codex-core`, `just fix -p codex-tui`, and `just fix -p codex-app-server` for touched crates |
| **Full suite command** | `cargo test -p codex-core compact && cargo test -p codex-tui && cargo test -p codex-app-server compaction` |
| **Estimated runtime** | ~360 seconds |

---

## Sampling Rate

- **After every task commit:** Run the narrowest relevant crate tests for the files changed in that task
- **After every plan wave:** Run `cargo test -p codex-core compact && cargo test -p codex-tui && cargo test -p codex-app-server compaction`
- **Before `$gsd-verify-work`:** Ask the user before running workspace `cargo test`, then require it to be green because Phase 5 is expected to touch `codex-core`
- **Max feedback latency:** 360 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 5-01-01 | 01 | 1 | RUN-03 | integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 5-01-02 | 01 | 1 | RUN-03 | lint/regression | `just fix -p codex-core` | ✅ | ⬜ pending |
| 5-02-01 | 02 | 2 | RUN-03 | integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 5-02-02 | 02 | 2 | RUN-03 | integration | `cargo test -p codex-app-server compaction` | ✅ | ⬜ pending |
| 5-03-01 | 03 | 2 | VIS-01, VIS-02 | snapshot/integration | `cargo test -p codex-tui` | ✅ | ⬜ pending |
| 5-03-02 | 03 | 2 | VIS-01, VIS-02 | snapshot review | `cargo insta pending-snapshots -p codex-tui` | ✅ | ⬜ pending |
| 5-04-01 | 04 | 3 | RUN-03, VIS-01, VIS-02 | regression | `cargo test -p codex-core compact && cargo test -p codex-tui && cargo test -p codex-app-server compaction` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Existing infrastructure covers all phase requirements.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Review accepted TUI snapshot diffs for the below-input compaction indicator wording and placement | VIS-01, VIS-02 | Snapshot tests prove rendering changed, but a human still needs to confirm the footer placement reads like a lightweight indicator rather than transcript chatter | Run `cargo test -p codex-tui`, inspect any `*.snap.new` files tied to compaction/footer rendering, then accept only the intended snapshots |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 360s
- [x] `nyquist_compliant: true` set in frontmatter
- [x] Complete-suite guidance matches repo rules: touched-crate tests run automatically, workspace `cargo test` stays user-gated

**Approval:** ready for planning
