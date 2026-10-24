//go:build unix

package store

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process with this PID is running. Signal
// 0 checks without sending anything; EPERM means it runs as another user.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
