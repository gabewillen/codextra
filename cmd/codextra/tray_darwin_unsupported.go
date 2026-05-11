//go:build darwin && cgo

package main

/*
#error "codextra: CGO-enabled macOS builds are not supported for tray. Build with CGO_ENABLED=0."
*/
import "C"
