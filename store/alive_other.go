//go:build !unix && !windows

package store

// processAlive can't tell on this system, so it assumes the process runs:
// locks and owner files then expire by age only.
func processAlive(int) bool { return true }
