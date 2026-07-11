//go:build !darwin

package main

import "errors"

func desktopAppCommand(_ []string) (string, []string, error) {
	return "", nil, errors.New("codextra --desktop is only supported on macOS")
}

func desktopAppShouldKeepAlive(_ []string) bool { return false }
