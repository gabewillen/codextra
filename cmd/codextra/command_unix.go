//go:build unix

package main

import (
	"os"
	"os/exec"
)

func configureCommandProcess(cmd *exec.Cmd) {
	// Keep the child in the same process group for terminal-based interactive behavior.
}

func signalCommandProcess(cmd *exec.Cmd) {
	_ = cmd.Process.Signal(os.Interrupt)
}

func killCommandProcess(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}
