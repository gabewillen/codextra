//go:build darwin && !cgo

package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gabewillen/codextra/internal/accounts"
)

func TestUsageBarHasFixedCellWidth(t *testing.T) {
	t.Parallel()

	for _, percent := range []int{-10, 0, 1, 13, 50, 72, 99, 100, 150} {
		got := usageBar(percent, 16)
		if n := utf8.RuneCountInString(got); n != 16 {
			t.Fatalf("usageBar(%d, 16) width = %d (%q), want 16", percent, n, got)
		}
	}

	if got := usageBar(50, 0); got != "" {
		t.Fatalf("usageBar(50, 0) = %q, want empty", got)
	}
}

func TestUsageBarFillsProportionally(t *testing.T) {
	t.Parallel()

	if got := usageBar(0, 10); got != strings.Repeat("░", 10) {
		t.Fatalf("usageBar(0, 10) = %q, want empty track", got)
	}
	if got := usageBar(100, 10); got != strings.Repeat("█", 10) {
		t.Fatalf("usageBar(100, 10) = %q, want full bar", got)
	}
	if got := usageBar(50, 10); got != strings.Repeat("█", 5)+strings.Repeat("░", 5) {
		t.Fatalf("usageBar(50, 10) = %q, want half full", got)
	}
}

func TestUsageBarShowsSubCellFillForLowUsage(t *testing.T) {
	t.Parallel()

	// 1% over a 10-cell meter is 0.8 of a cell — should render a partial block,
	// not snap down to an empty meter or up to a whole cell.
	got := usageBar(1, 10)
	if strings.HasPrefix(got, "█") {
		t.Fatalf("usageBar(1, 10) = %q, want a partial leading block, not a full cell", got)
	}
	if !strings.ContainsAny(got, "▏▎▍▌▋▊▉") {
		t.Fatalf("usageBar(1, 10) = %q, want a sub-cell partial block", got)
	}
}

func TestSignedOutAccountLabelCuesSignIn(t *testing.T) {
	t.Parallel()

	now := time.Now()
	label := formatAccountMenuLabel(accounts.Account{Alias: "work"}, "", now)
	if !strings.Contains(label, "Sign in") {
		t.Fatalf("signed-out label = %q, want a Sign in cue", label)
	}
	if !strings.Contains(label, "🔴") {
		t.Fatalf("signed-out label = %q, want the needs-sign-in glyph", label)
	}

	ready := formatAccountMenuLabel(accounts.Account{Alias: "work", AccessToken: "t"}, "work", now)
	if !strings.Contains(ready, "🟢") {
		t.Fatalf("ready current label = %q, want the ready glyph", ready)
	}
}
