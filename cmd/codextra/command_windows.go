//go:build windows

package main

import (
	"os"
	"os/exec"
)

func configureCommandProcess(cmd *exec.Cmd) {
	// No process-group customization available on this platform in this wrapper.
}

func signalCommandProcess(cmd *exec.Cmd) {
	_ = cmd.Process.Signal(os.Interrupt)
}

func killCommandProcess(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}
