---
phase: 04
slug: failure-recovery-and-blocking-guardrails
status: ready
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-09
---

# Phase 04 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Rust `cargo test` integration + unit suites |
| **Config file** | none — existing crate test infrastructure |
| **Quick run command** | `cargo test -p codex-core compact` |
| **Full suite command** | `cargo test -p codex-core compact && cargo test -p codex-app-server compaction` |
| **Estimated runtime** | ~120 seconds |

---

## Sampling Rate

- **After every task commit:** Run `cargo test -p codex-core compact`
- **After every plan wave:** Run `cargo test -p codex-core compact && cargo test -p codex-app-server compaction`
- **Before `$gsd-verify-work`:** Full suite must be green
- **Max feedback latency:** 120 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 04-01-01 | 01 | 1 | RECV-01 | integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 04-01-02 | 01 | 1 | RECV-02 | unit/integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 04-02-01 | 02 | 2 | COMP-01 | integration | `cargo test -p codex-core compact && cargo test -p codex-app-server compaction` | ✅ | ⬜ pending |
| 04-02-02 | 02 | 2 | COMP-02 | integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 04-03-01 | 03 | 3 | RECV-01, RECV-02, COMP-01, COMP-02 | regression | `cargo test -p codex-core compact && cargo test -p codex-app-server compaction` | ✅ | ⬜ pending |

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
- [x] Feedback latency < 120s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** ready for planning
