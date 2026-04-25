//go:build windows

package cli

import "os"

// sameHostPIDAliveOS relies on os.FindProcess actually validating
// existence on Windows (unlike POSIX where it always succeeds). The
// Go runtime opens the process via OpenProcess; a nonexistent PID
// returns an error.
//
// This is a coarser check than the POSIX Signal(0) probe — a zombie
// or recently-exited process may still be reachable via OpenProcess.
// For our purposes (reclaim a stale scheduler_lock from a crashed
// surfbot run) the heartbeat-staleness fallback covers the gap.
func sameHostPIDAliveOS(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.FindProcess(pid)
	return err == nil
}
