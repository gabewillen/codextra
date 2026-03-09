---
phase: 3
slug: durable-history-and-surface-compatibility
status: ready
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-09
---

# Phase 3 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Rust `cargo test` integration + unit tests |
| **Config file** | `codex-rs/Cargo.toml` |
| **Quick run command** | `cargo test -p codex-core compact && cargo test -p codex-app-server-protocol thread_history && cargo test -p codex-app-server compaction && cargo test -p codex-app-server thread_rollback` |
| **Scoped lint/fix command** | `just fix -p codex-core` for core-only waves; `just fix -p codex-app-server-protocol` / `just fix -p codex-app-server` if those crates change materially |
| **Full suite command** | project-specific suites for touched crates during execution; workspace `cargo test` remains user-gated because Phase 3 is expected to touch `codex-core` |
| **Estimated runtime** | ~240 seconds for scoped checks |

---

## Sampling Rate

- **After every task commit:** Run the narrowest relevant project tests for the touched crate(s)
- **After every plan wave:** Run the relevant project-specific suites for the files changed in that wave
- **Before `$gsd-verify-work`:** Ask the user before running workspace `cargo test`, then require it to be green because Phase 3 is expected to touch `codex-core`
- **Max feedback latency:** 240 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 3-01-01 | 01 | 1 | HIST-03 | unit/integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 3-01-02 | 01 | 1 | HIST-03 | lint/regression | `just fix -p codex-core` | ✅ | ⬜ pending |
| 3-02-01 | 02 | 2 | HIST-03,COMP-03 | protocol/unit | `cargo test -p codex-app-server-protocol thread_history` | ✅ | ⬜ pending |
| 3-02-02 | 02 | 2 | COMP-03 | integration | `cargo test -p codex-app-server compaction && cargo test -p codex-app-server thread_rollback` | ✅ | ⬜ pending |
| 3-02-03 | 02 | 2 | HIST-03,COMP-03 | lint/regression | `just fix -p codex-app-server-protocol && just fix -p codex-app-server` | ✅ | ⬜ pending |
| 3-03-01 | 03 | 3 | HIST-03,COMP-03 | regression | `cargo test -p codex-core compact && cargo test -p codex-app-server compaction && cargo test -p codex-app-server thread_rollback` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Existing infrastructure covers all phase requirements.

---

## Manual-Only Verifications

All phase behaviors have automated verification.

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 240s
- [x] `nyquist_compliant: true` set in frontmatter
- [x] Complete-suite guidance matches repo rules: project-specific tests run automatically, workspace `cargo test` stays user-gated

**Approval:** ready for planning
