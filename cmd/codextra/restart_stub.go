//go:build !darwin && !linux

package main

import "context"

func startRestartSignalWatcher(ctx context.Context, onRestart func()) func() {
	return func() {}
}

// reexecSelf is unused on platforms without the SIGUSR1 upgrade path; the
// restart is never requested there, so this is a no-op fallback.
func reexecSelf() error {
	return nil
}

// ignoreUpgradeSignal is a no-op where the upgrade SIGUSR1 path is unsupported.
func ignoreUpgradeSignal() {}
