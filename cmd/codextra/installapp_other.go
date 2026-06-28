//go:build !darwin

package main

import "errors"

// installDesktopApp is macOS-only; the clickable launcher is an .app bundle.
func installDesktopApp() error {
	return errors.New("codextra install-app is only supported on macOS")
}
