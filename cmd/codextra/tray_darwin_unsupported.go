//go:build darwin && cgo

package main

import (
	"context"
	"log"
)

func startTray(ctx context.Context, storePath string, onActivate func(string) error) func() {
	log.Printf("codextra tray is disabled because this codextra build was linked with CGO enabled")
	log.Printf("Build tray-capable binary with CGO_ENABLED=0 on macOS.")
	return func() {}
}
