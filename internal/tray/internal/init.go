//go:build darwin

package internal

import "runtime"

func init() {
	runtime.LockOSThread()
}
