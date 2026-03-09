---
phase: 1
slug: background-trigger-and-continued-turns
status: draft
nyquist_compliant: false
wave_0_complete: false
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
| **Full suite command** | `cargo test -p codex-core && cargo test -p codex-app-server compaction` |
| **Estimated runtime** | ~180 seconds |

---

## Sampling Rate

- **After every task commit:** Run `cargo test -p codex-core compact`
- **After every plan wave:** Run `cargo test -p codex-core && cargo test -p codex-app-server compaction`
- **Before `$gsd-verify-work`:** Full suite must be green
- **Max feedback latency:** 180 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 1-01-01 | 01 | 1 | RUN-01 | integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 1-01-02 | 01 | 1 | RUN-02 | integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 1-02-01 | 02 | 1 | RUN-01 | state/integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 1-02-02 | 02 | 1 | RUN-02 | integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 1-03-01 | 03 | 2 | RUN-01 | protocol/integration | `cargo test -p codex-app-server compaction` | ✅ | ⬜ pending |
| 1-03-02 | 03 | 2 | RUN-02 | regression | `cargo test -p codex-core && cargo test -p codex-app-server compaction` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `codex-rs/core/tests/suite/compact.rs` — add delayed background-compaction coverage for `RUN-01`
- [ ] `codex-rs/core/tests/suite/compact_remote.rs` — add delayed remote background-compaction coverage for `RUN-01`
- [ ] `codex-rs/app-server/tests/suite/v2/compaction.rs` — cover event ordering and no-interrupt regression for `RUN-02`

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Long-running TUI session continues visibly while auto-compaction is active | RUN-02 | The eventual transcript/UI feel is easiest to judge interactively | Run a long tool-heavy session in the TUI, trigger auto-compaction, and confirm the agent keeps streaming output while compaction is in flight |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 180s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
