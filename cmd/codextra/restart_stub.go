//go:build !darwin && !linux

package main

import "context"

func startRestartSignalWatcher(ctx context.Context, onRestart func()) func() {
	return func() {}
}
