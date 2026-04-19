// Package common houses the shared plumbing (config, output rendering,
// TTY confirmation, API-error → exit-code translation) every SCHED1.3b
// CLI subcommand uses. Kept small and focused — if a helper doesn't
// serve at least two subcommands, it belongs in the subcommand's file.
package common

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"

	"github.com/surfbot-io/surfbot-agent/internal/daemon"
	"github.com/surfbot-io/surfbot-agent/internal/webui"
)

// DefaultDaemonPort mirrors the webui package's `surfbot ui -p` default
// (8470). Kept as a package constant rather than reaching into internal/
// so the CLI consumer doesn't tangle with UI server internals.
const DefaultDaemonPort = 8470

// DefaultDaemonURL is the base URL used when no flag, env var, or
// config file entry overrides it. Loopback is the only reachable value
// for the embedded UI server — remote access requires a reverse proxy
// that operators provision outside this binary.
const DefaultDaemonURL = "http://127.0.0.1:8470"

// APIConfig bundles the resolved connection settings every CLI
// subcommand hands to apiclient.New.
type APIConfig struct {
	BaseURL   string
	AuthToken string
}

// ResolveAPIConfig picks the daemon URL and auth token using the
// precedence: explicit flag > SURFBOT_DAEMON_URL env > config-file
// `daemon_url` key > DefaultDaemonURL. AuthToken is read from the
// user-mode ui.token file so the embedded UI server accepts the
// request; callers pointing at a bare daemon can ignore the token.
//
// flagURL is the value of a --daemon-url flag; pass "" when the
// subcommand does not expose one.
func ResolveAPIConfig(flagURL string) APIConfig {
	cfg := APIConfig{BaseURL: resolveBaseURL(flagURL)}
	cfg.AuthToken = resolveAuthToken()
	return cfg
}

func resolveBaseURL(flagURL string) string {
	if flagURL != "" {
		return flagURL
	}
	if v := os.Getenv("SURFBOT_DAEMON_URL"); v != "" {
		return v
	}
	// Viper is already bootstrapped by the root command; readers here
	// tolerate the case where Viper's config wasn't loaded.
	if v := viper.GetString("daemon_url"); v != "" {
		return v
	}
	return DefaultDaemonURL
}

// resolveAuthToken reads the ui.token file from the user-mode state
// dir if it exists. Errors collapse to "no token" — callers get a
// 401 from the server instead of a crash here, and the 401 message
// tells the operator exactly how to fix it.
func resolveAuthToken() string {
	if v := os.Getenv("SURFBOT_AUTH_TOKEN"); v != "" {
		return v
	}
	paths := daemon.Resolve(daemon.Default(daemon.ModeUser))
	tokenPath := filepath.Join(paths.StateDir, "ui.token")
	tok, err := webui.LoadOrCreateUIToken(paths.StateDir)
	if err != nil {
		// LoadOrCreateUIToken tries to create a new one if the dir
		// doesn't exist. If even that fails (e.g., readonly FS),
		// give up silently — the caller will see a 401.
		_ = tokenPath
		return ""
	}
	return tok
}

// DisplayURL returns a compact string for the "couldn't reach X" error
// path — uses the effective base URL, never a value that leaked from
// a secret-bearing env var.
func (c APIConfig) DisplayURL() string {
	if c.BaseURL == "" {
		return DefaultDaemonURL
	}
	return c.BaseURL
}

// Sprintf is a small helper the render/confirm/errors files use to
// keep imports small. Kept here to avoid pulling fmt into every file.
func Sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
