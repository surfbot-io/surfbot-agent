// Package daemon provides background service support for surfbot using
// kardianos/service. It exposes per-OS path resolution, an atomic state
// store, a rotating logger, and a Scheduler interface stubbed by NoopScheduler
// (the real scheduler ships in SPEC-X2).
package daemon

import (
	"os"
	"path/filepath"
	"runtime"
)

// Mode selects between system-wide and per-user installation.
type Mode int

const (
	ModeSystem Mode = iota
	ModeUser
)

func (m Mode) String() string {
	if m == ModeUser {
		return "user"
	}
	return "system"
}

// DefaultMode returns the recommended install mode for the current OS.
// macOS defaults to user (LaunchAgent); everything else to system.
func DefaultMode() Mode {
	if runtime.GOOS == "darwin" {
		return ModeUser
	}
	return ModeSystem
}

// Paths holds the resolved filesystem locations the daemon writes to.
type Paths struct {
	ConfigPath string // config.yaml
	StateDir   string // contains daemon.state.json
	LogDir     string // contains daemon.log
}

// StateFile returns the absolute path to the state JSON file.
func (p Paths) StateFile() string { return filepath.Join(p.StateDir, "daemon.state.json") }

// LogFile returns the absolute path to the daemon log file.
func (p Paths) LogFile() string { return filepath.Join(p.LogDir, "daemon.log") }

// Options is the injectable input for Resolve. Tests pass synthetic values;
// production code uses Default().
type Options struct {
	GOOS        string
	Mode        Mode
	Home        string // user home dir
	ProgramData string // %ProgramData% on Windows
}

// Default returns Options derived from the current process environment.
func Default(mode Mode) Options {
	home, _ := os.UserHomeDir()
	return Options{
		GOOS:        runtime.GOOS,
		Mode:        mode,
		Home:        home,
		ProgramData: os.Getenv("ProgramData"),
	}
}

// Resolve returns Paths for the given options. It is a pure function so
// tests can exercise every (GOOS, Mode) combination without touching the
// real filesystem.
func Resolve(o Options) Paths {
	switch o.GOOS {
	case "linux":
		if o.Mode == ModeUser {
			state := filepath.Join(o.Home, ".local", "state", "surfbot")
			return Paths{
				ConfigPath: filepath.Join(o.Home, ".config", "surfbot", "config.yaml"),
				StateDir:   state,
				LogDir:     filepath.Join(state, "logs"),
			}
		}
		return Paths{
			ConfigPath: "/etc/surfbot/config.yaml",
			StateDir:   "/var/lib/surfbot",
			LogDir:     "/var/log/surfbot",
		}
	case "darwin":
		if o.Mode == ModeUser {
			base := filepath.Join(o.Home, "Library", "Application Support", "surfbot")
			return Paths{
				ConfigPath: filepath.Join(base, "config.yaml"),
				StateDir:   base,
				LogDir:     filepath.Join(o.Home, "Library", "Logs", "surfbot"),
			}
		}
		return Paths{
			ConfigPath: "/Library/Application Support/surfbot/config.yaml",
			StateDir:   "/Library/Application Support/surfbot",
			LogDir:     "/Library/Logs/surfbot",
		}
	case "windows":
		base := o.ProgramData
		if base == "" {
			base = `C:\ProgramData`
		}
		base = filepath.Join(base, "surfbot")
		return Paths{
			ConfigPath: filepath.Join(base, "config.yaml"),
			StateDir:   filepath.Join(base, "state"),
			LogDir:     filepath.Join(base, "logs"),
		}
	default:
		// Fallback: stick everything under the user home.
		base := filepath.Join(o.Home, ".surfbot", "daemon")
		return Paths{
			ConfigPath: filepath.Join(o.Home, ".surfbot", "config.yaml"),
			StateDir:   base,
			LogDir:     filepath.Join(base, "logs"),
		}
	}
}

// EnsureDirs creates the state and log directories with mode-appropriate
// permissions. System mode → 0750, user mode → 0700.
func (p Paths) EnsureDirs(mode Mode) error {
	perm := os.FileMode(0o750)
	if mode == ModeUser {
		perm = 0o700
	}
	for _, d := range []string{p.StateDir, p.LogDir} {
		if err := os.MkdirAll(d, perm); err != nil {
			return err
		}
	}
	return nil
}
