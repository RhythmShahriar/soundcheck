//go:build windows

package store

import "os"

// processAlive reports whether a process with this PID is running. On
// Windows FindProcess opens the process, which fails once it's gone.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
