//go:build !windows

package cli

import (
	"os"
	"syscall"
)

// sameHostPIDAliveOS uses POSIX signal 0: it performs the permission /
// existence check without actually delivering a signal. Returns true
// iff the process exists and we have permission to signal it.
func sameHostPIDAliveOS(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
