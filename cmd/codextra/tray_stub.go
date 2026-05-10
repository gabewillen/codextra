//go:build !darwin || cgo

package main

import "context"

func startTray(ctx context.Context, storePath string, onActivate func(string) error) func() {
	return func() {}
}
