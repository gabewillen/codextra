//go:build darwin && !cgo

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
)

func TestSignedOutAccountLabelCuesSignIn(t *testing.T) {
	t.Parallel()

	now := time.Now()
	label := formatAccountMenuLabel(accounts.Account{Alias: "work"}, now)
	if !strings.Contains(label, "Sign in") {
		t.Fatalf("signed-out label = %q, want a Sign in cue", label)
	}
	if !strings.Contains(label, "🔴") {
		t.Fatalf("signed-out label = %q, want the needs-sign-in glyph", label)
	}

	ready := formatAccountMenuLabel(accounts.Account{Alias: "work", AccessToken: "t"}, now)
	if !strings.Contains(ready, "🟢") {
		t.Fatalf("ready label = %q, want the ready glyph", ready)
	}
}

func TestUsageWindowLineMirrorsCodex(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.Local)

	// A 5h window resetting later today shows a clock time.
	soon := now.Add(3 * time.Hour)
	line := usageWindowLine(accounts.UsageWindow{Label: "5h", Percent: 12, ResetAt: soon.Unix()}, now)
	if !strings.Contains(line, "5h") || !strings.Contains(line, "12%") {
		t.Fatalf("line = %q, want label and percent", line)
	}
	if !strings.Contains(line, soon.Format("3:04 PM")) {
		t.Fatalf("line = %q, want a clock-time reset", line)
	}

	// A weekly window resetting days out shows a date, not a time.
	later := now.Add(5 * 24 * time.Hour)
	weekly := usageWindowLine(accounts.UsageWindow{Label: "Weekly", Percent: 99, ResetAt: later.Unix()}, now)
	if !strings.Contains(weekly, later.Format("Jan 2")) {
		t.Fatalf("weekly line = %q, want a date reset", weekly)
	}
}

func TestCurrentAccountUsageLinesFallback(t *testing.T) {
	t.Parallel()

	now := time.Now()
	lines := currentAccountUsageLines(accounts.Account{Alias: "work", AccessToken: "t"}, now)
	if len(lines) != 1 || !strings.Contains(lines[0], "unavailable") {
		t.Fatalf("no-usage lines = %#v, want a single 'unavailable' placeholder", lines)
	}

	withUsage := accounts.Account{Alias: "work", AccessToken: "t", Usage: []accounts.UsageWindow{
		{Label: "5h", Percent: 0},
		{Label: "Weekly", Percent: 1},
	}}
	lines = currentAccountUsageLines(withUsage, now)
	if len(lines) != 2 {
		t.Fatalf("usage lines = %#v, want one per window", lines)
	}
}
