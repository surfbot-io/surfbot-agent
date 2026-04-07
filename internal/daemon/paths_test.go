package daemon

import (
	"strings"
	"testing"
)

func TestResolve_AllOS(t *testing.T) {
	cases := []struct {
		name        string
		opts        Options
		wantContain []string // substrings that must appear in StateDir
	}{
		{
			name: "linux system",
			opts: Options{GOOS: "linux", Mode: ModeSystem, Home: "/home/u"},
			wantContain: []string{
				"/var/lib/surfbot",
				"/var/log/surfbot",
				"/etc/surfbot/config.yaml",
			},
		},
		{
			name: "linux user",
			opts: Options{GOOS: "linux", Mode: ModeUser, Home: "/home/u"},
			wantContain: []string{
				"/home/u/.local/state/surfbot",
				"/home/u/.config/surfbot/config.yaml",
			},
		},
		{
			name: "darwin user",
			opts: Options{GOOS: "darwin", Mode: ModeUser, Home: "/Users/u"},
			wantContain: []string{
				"/Users/u/Library/Application Support/surfbot",
				"/Users/u/Library/Logs/surfbot",
			},
		},
		{
			name: "darwin system",
			opts: Options{GOOS: "darwin", Mode: ModeSystem, Home: "/Users/u"},
			wantContain: []string{
				"/Library/Application Support/surfbot",
				"/Library/Logs/surfbot",
			},
		},
		{
			name: "windows",
			opts: Options{GOOS: "windows", Mode: ModeSystem, ProgramData: `C:\ProgramData`},
			wantContain: []string{
				"surfbot",
				"state",
				"logs",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Resolve(tc.opts)
			joined := p.ConfigPath + "|" + p.StateDir + "|" + p.LogDir
			for _, sub := range tc.wantContain {
				if !strings.Contains(joined, sub) {
					t.Errorf("expected %q in %q", sub, joined)
				}
			}
			if p.StateFile() == "" || p.LogFile() == "" {
				t.Error("StateFile/LogFile must be non-empty")
			}
		})
	}
}

func TestDefaultMode(t *testing.T) {
	// Just smoke-test that DefaultMode returns a valid value.
	m := DefaultMode()
	if m != ModeSystem && m != ModeUser {
		t.Errorf("unexpected mode %v", m)
	}
}
