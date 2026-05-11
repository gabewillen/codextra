//go:build darwin || linux

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

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
