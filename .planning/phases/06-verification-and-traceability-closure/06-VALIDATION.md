---
phase: 06
slug: verification-and-traceability-closure
status: ready
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-09
---

# Phase 06 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | shell artifact checks + milestone audit workflow |
| **Config file** | none - existing `.planning/` artifacts |
| **Quick run command** | `find .planning/phases -maxdepth 2 -name '*-VERIFICATION.md' | sort` |
| **Full suite command** | `find .planning/phases -maxdepth 2 -name '*-VERIFICATION.md' | sort && rg -n '^status: (passed|human_needed)$' .planning/phases/*/*-VERIFICATION.md && rg -n '^wave_0_complete: true$' .planning/phases/02-safe-transcript-splicing/02-VALIDATION.md && rg -n '^status: ready$' .planning/phases/04-failure-recovery-and-blocking-guardrails/04-VALIDATION.md` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `find .planning/phases -maxdepth 2 -name '*-VERIFICATION.md' | sort`
- **After every plan wave:** Run `find .planning/phases -maxdepth 2 -name '*-VERIFICATION.md' | sort && rg -n '^status: (passed|human_needed)$' .planning/phases/*/*-VERIFICATION.md && rg -n '^wave_0_complete: true$' .planning/phases/02-safe-transcript-splicing/02-VALIDATION.md && rg -n '^status: ready$' .planning/phases/04-failure-recovery-and-blocking-guardrails/04-VALIDATION.md`
- **Before `$gsd-verify-work`:** The rerun milestone audit must be clean
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 06-01-01 | 01 | 1 | RUN-01,RUN-02,HIST-01,HIST-02,HIST-03,COMP-03 | artifact | `find .planning/phases -maxdepth 2 -name '*-VERIFICATION.md' | sort` | ✅ | ⬜ pending |
| 06-02-01 | 02 | 2 | RECV-01,RECV-02,COMP-01,COMP-02,RUN-03,VIS-01,VIS-02 | artifact | `find .planning/phases -maxdepth 2 -name '*-VERIFICATION.md' | sort && rg -n '^wave_0_complete: true$' .planning/phases/02-safe-transcript-splicing/02-VALIDATION.md && rg -n '^status: ready$' .planning/phases/04-failure-recovery-and-blocking-guardrails/04-VALIDATION.md` | ✅ | ⬜ pending |
| 06-03-01 | 03 | 3 | RUN-01,RUN-02,RUN-03,HIST-01,HIST-02,HIST-03,RECV-01,RECV-02,VIS-01,VIS-02,COMP-01,COMP-02,COMP-03 | workflow | `rg -n '^status: (passed|human_needed)$' .planning/phases/*/*-VERIFICATION.md && rg -n '^\\- \\[x\\] \\*\\*(RUN|HIST|RECV|VIS|COMP)-' .planning/REQUIREMENTS.md` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Existing infrastructure covers all phase requirements.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Final milestone audit status is clean | RUN-01,RUN-02,RUN-03,HIST-01,HIST-02,HIST-03,RECV-01,RECV-02,VIS-01,VIS-02,COMP-01,COMP-02,COMP-03 | The workflow command rewrites the audit artifact and is not represented by a single stable shell assertion here | Run `$gsd-audit-milestone` after Plan 03 and confirm the report no longer shows requirement, integration, or flow gaps |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 15s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
