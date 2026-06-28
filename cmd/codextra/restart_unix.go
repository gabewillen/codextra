//go:build darwin || linux

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// reexecSelf replaces the current process image with a fresh exec of the
// codextra binary so an upgrade restart actually picks up the new on-disk
// build instead of re-running the already-mapped executable. On success it
// never returns.
func reexecSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}

// ignoreUpgradeSignal makes a process disregard the upgrade SIGUSR1. The
// detached serve-proxy child shares the codextra binary name, so the
// installer's `pkill -USR1 -x codextra` also hits it; without this it would
// take SIGUSR1's default action and terminate the proxy mid-install.
func ignoreUpgradeSignal() {
	signal.Ignore(syscall.SIGUSR1)
}

func startRestartSignalWatcher(ctx context.Context, onRestart func()) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)

	watchCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ch:
				onRestart()
			}
		}
	}()

	return func() {
		signal.Stop(ch)
		cancel()
		<-done
	}
}
