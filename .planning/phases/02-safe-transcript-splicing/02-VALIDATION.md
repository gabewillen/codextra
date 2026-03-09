---
phase: 2
slug: safe-transcript-splicing
status: ready
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-09
---

# Phase 2 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Rust `cargo test` integration + unit tests |
| **Config file** | none — existing workspace test setup |
| **Quick run command** | `cargo test -p codex-core compact` |
| **Full suite command** | `cargo test -p codex-core compact` |
| **Estimated runtime** | ~20 seconds |

---

## Sampling Rate

- **After every task commit:** Run `cargo test -p codex-core compact`
- **After every plan wave:** Run `cargo test -p codex-core compact`
- **Before `$gsd-verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 2-01-01 | 01 | 1 | HIST-01 | unit | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 2-02-01 | 02 | 2 | HIST-01,HIST-02 | integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |
| 2-03-01 | 03 | 3 | HIST-01,HIST-02 | integration | `cargo test -p codex-core compact` | ✅ | ⬜ pending |

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
- [x] Feedback latency < 30s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** ready for planning
