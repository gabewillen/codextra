//go:build darwin && !cgo

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopLauncherScriptBakesPaths(t *testing.T) {
	t.Parallel()

	script := desktopLauncherScript("/usr/local/bin/codextra", "/opt/homebrew/bin/codex")
	if !strings.Contains(script, "exec '/usr/local/bin/codextra' --desktop") {
		t.Fatalf("script missing codextra exec line:\n%s", script)
	}
	if !strings.Contains(script, "CODEXTRA_CODEX_BIN='/opt/homebrew/bin/codex'") {
		t.Fatalf("script missing baked codex bin:\n%s", script)
	}

	// With no codex found, the launcher omits the override and relies on PATH.
	noCodex := desktopLauncherScript("/usr/local/bin/codextra", "")
	if strings.Contains(noCodex, "CODEXTRA_CODEX_BIN") {
		t.Fatalf("script should omit codex override when unresolved:\n%s", noCodex)
	}
}

func TestShellSingleQuoteEscapesQuotes(t *testing.T) {
	t.Parallel()

	got := shellSingleQuote(`/odd/pa'th/codex`)
	want := `'/odd/pa'\''th/codex'`
	if got != want {
		t.Fatalf("shellSingleQuote = %q, want %q", got, want)
	}
}

func TestWriteDesktopAppBundleCreatesLauncherAndPlist(t *testing.T) {
	t.Parallel()

	appPath := filepath.Join(t.TempDir(), "Codextra.app")
	if err := writeDesktopAppBundle(appPath, "/bin/codextra", "/bin/codex"); err != nil {
		t.Fatalf("writeDesktopAppBundle() error = %v", err)
	}

	plist, err := os.ReadFile(filepath.Join(appPath, "Contents", "Info.plist"))
	if err != nil {
		t.Fatalf("read Info.plist: %v", err)
	}
	if !strings.Contains(string(plist), "<string>codextra-desktop</string>") {
		t.Fatalf("Info.plist missing CFBundleExecutable")
	}

	launcher := filepath.Join(appPath, "Contents", "MacOS", "codextra-desktop")
	info, err := os.Stat(launcher)
	if err != nil {
		t.Fatalf("stat launcher: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("launcher not executable: %v", info.Mode())
	}
}

func TestAppIconPNGRenders(t *testing.T) {
	t.Parallel()

	// A small render should still produce a non-empty PNG with the logo on it.
	data, err := appIconPNG(64)
	if err != nil {
		t.Fatalf("appIconPNG() error = %v", err)
	}
	if len(data) == 0 {
		t.Fatal("appIconPNG produced no data")
	}
}
