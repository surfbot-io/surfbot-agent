package cli

import (
	"testing"

	"github.com/surfbot-io/surfbot-agent/internal/daemon"
)

func TestResolveDaemonDBPath_SystemModeIgnoresHome(t *testing.T) {
	t.Setenv("HOME", "")
	paths := daemon.Paths{StateDir: "/var/lib/surfbot", LogDir: "/var/log/surfbot"}
	got := resolveDaemonDBPath(daemon.ModeSystem, paths, "/root/.surfbot/surfbot.db")
	want := "/var/lib/surfbot/surfbot.db"
	if got != want {
		t.Fatalf("system mode DB path = %q, want %q", got, want)
	}
}

func TestResolveDaemonDBPath_UserModeKeepsConfigured(t *testing.T) {
	paths := daemon.Paths{StateDir: "/home/u/.local/state/surfbot"}
	configured := "/home/u/.surfbot/surfbot.db"
	if got := resolveDaemonDBPath(daemon.ModeUser, paths, configured); got != configured {
		t.Fatalf("user mode DB path = %q, want %q", got, configured)
	}
}
