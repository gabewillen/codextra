//go:build darwin

package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// desktopAppCommand starts the current macOS Codex application. Although the
// bundle is named ChatGPT.app, its bundle identifier is com.openai.codex and it
// is the supported Codex desktop app.
func desktopAppCommand(userArgs []string) (string, []string, error) {
	app, err := defaultCodexAppExecutable()
	if err != nil {
		return "", nil, err
	}
	return app, []string{codexDesktopOpenURL(userArgs)}, nil
}

func defaultCodexAppExecutable() (string, error) {
	candidates := []string{"/Applications/ChatGPT.app"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "Applications", "ChatGPT.app"))
	}
	for _, app := range candidates {
		executable := filepath.Join(app, "Contents", "MacOS", "ChatGPT")
		if info, err := os.Stat(executable); err == nil && !info.IsDir() {
			return executable, nil
		}
	}
	return "", fmt.Errorf("codex desktop app not found; install the current ChatGPT.app Codex application")
}

func codexDesktopOpenURL(userArgs []string) string {
	workspace := "."
	for _, arg := range userArgs {
		if arg == "--" {
			break
		}
		if len(arg) > 0 && arg[0] != '-' {
			workspace = arg
			break
		}
	}
	query := url.Values{"path": []string{workspace}}
	return "codex://threads/new?" + query.Encode()
}

func desktopAppShouldKeepAlive(_ []string) bool {
	// The app executable remains attached until the user quits it, so the
	// parent proxy naturally stays alive for exactly the app's lifetime.
	return false
}
