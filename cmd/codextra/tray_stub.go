//go:build !darwin

package main

import (
	"context"
	"log"
)

func startTray(ctx context.Context, storePath, proxyURL string, onActivate func(string) error, onLogin func(string) error) func() {
	log.Printf("codextra tray: stub platform (non-darwin), tray is disabled")
	return func() {}
}

// takeTrayRunner has no tray event loop to surrender on non-darwin platforms.
func takeTrayRunner() func() error {
	return nil
}
