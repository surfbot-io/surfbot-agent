package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/daemon"
)

// Persistent flags on the parent `daemon` command. They control the install
// mode for every subcommand so that `daemon install`, `daemon status`, and
// `daemon run` all agree on which paths and which kardianos UserService
// setting to use.
var (
	daemonFlagSystem bool
	daemonFlagUser   bool
	daemonLogsFollow bool
	daemonLogsSince  string
	daemonStatusJSON bool
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Install and control the surfbot background service",
	Long: `Manage the surfbot agent as a long-running system service.

Install registers surfbot with the local service manager (systemd on
Linux, launchd on macOS, the Service Control Manager on Windows). Once
installed, the daemon stays running across reboots and triggers scheduled
scans defined in your config.

The default install mode depends on your OS:

  Linux    --system  (root, /etc/systemd/system/surfbot.service)
  macOS    --user    (~/Library/LaunchAgents/io.surfbot.plist)
  Windows  --system  (Service Control Manager, requires Administrator)`,
}

var daemonInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Register surfbot as a system service",
	RunE:  runDaemonInstall,
}

var daemonUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the surfbot service registration",
	RunE:  runDaemonUninstall,
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the installed surfbot service",
	RunE:  runDaemonStart,
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running surfbot service",
	RunE:  runDaemonStop,
}

var daemonRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the surfbot service",
	RunE:  runDaemonRestart,
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status, pid, version, and next scheduled scan",
	RunE:  runDaemonStatus,
}

var daemonLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Print or tail the daemon log file",
	RunE:  runDaemonLogs,
}

var daemonRunCmd = &cobra.Command{
	Use:    "run",
	Short:  "Internal: entrypoint invoked by the OS service manager",
	Hidden: true,
	RunE:   runDaemonRun,
}

func init() {
	daemonCmd.PersistentFlags().BoolVar(&daemonFlagSystem, "system", false, "Install/operate as a system-wide service (root)")
	daemonCmd.PersistentFlags().BoolVar(&daemonFlagUser, "user", false, "Install/operate as a per-user service")

	daemonStatusCmd.Flags().BoolVar(&daemonStatusJSON, "json", false, "Output status as JSON")

	daemonLogsCmd.Flags().BoolVarP(&daemonLogsFollow, "follow", "f", false, "Tail the log file")
	daemonLogsCmd.Flags().StringVar(&daemonLogsSince, "since", "", "Only show entries newer than this duration (e.g. 1h)")

	for _, c := range []*cobra.Command{
		daemonInstallCmd, daemonUninstallCmd, daemonStartCmd, daemonStopCmd,
		daemonRestartCmd, daemonStatusCmd, daemonLogsCmd, daemonRunCmd,
	} {
		daemonCmd.AddCommand(c)
	}
	rootCmd.AddCommand(daemonCmd)
}

// resolveMode picks the install mode from the explicit --system / --user
// flags, falling back to the per-OS default.
func resolveMode() (daemon.Mode, error) {
	if daemonFlagSystem && daemonFlagUser {
		return 0, errors.New("--system and --user are mutually exclusive")
	}
	if daemonFlagSystem {
		return daemon.ModeSystem, nil
	}
	if daemonFlagUser {
		return daemon.ModeUser, nil
	}
	return daemon.DefaultMode(), nil
}

// buildDaemonService is the common setup all daemon subcommands run.
func buildDaemonService() (*daemon.Service, service.Service, daemon.Mode, error) {
	mode, err := resolveMode()
	if err != nil {
		return nil, nil, 0, err
	}
	paths := daemon.Resolve(daemon.Default(mode))
	cfg := daemon.Config{
		Mode:           mode,
		Paths:          paths,
		Version:        Version,
		ShutdownGrace:  20 * time.Second,
		Heartbeat:      30 * time.Second,
		ConfigOverride: cfgFile,
	}
	s, svc, err := daemon.Build(cfg)
	if err != nil {
		return nil, nil, mode, err
	}
	return s, svc, mode, nil
}

func runDaemonInstall(cmd *cobra.Command, _ []string) error {
	_, svc, mode, err := buildDaemonService()
	if err != nil {
		return err
	}
	if err := svc.Install(); err != nil {
		// Idempotent: kardianos returns "Init already exists" or similar.
		if isAlreadyInstalled(err) {
			fmt.Fprintln(cmd.OutOrStdout(), "surfbot daemon already installed")
			return nil
		}
		if isPermissionError(err) {
			return fmt.Errorf("daemon install requires root/Administrator privileges. Try: sudo surfbot daemon install (original error: %w)", err)
		}
		return fmt.Errorf("installing service: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "surfbot daemon installed (%s mode)\n", mode)
	return nil
}

func runDaemonUninstall(cmd *cobra.Command, _ []string) error {
	_, svc, _, err := buildDaemonService()
	if err != nil {
		return err
	}
	if err := svc.Uninstall(); err != nil {
		if isPermissionError(err) {
			return fmt.Errorf("daemon uninstall requires root/Administrator privileges (original error: %w)", err)
		}
		return fmt.Errorf("uninstalling service: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "surfbot daemon uninstalled")
	return nil
}

func runDaemonStart(cmd *cobra.Command, _ []string) error {
	_, svc, _, err := buildDaemonService()
	if err != nil {
		return err
	}
	if err := svc.Start(); err != nil {
		return fmt.Errorf("starting service: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "surfbot daemon started")
	return nil
}

func runDaemonStop(cmd *cobra.Command, _ []string) error {
	_, svc, _, err := buildDaemonService()
	if err != nil {
		return err
	}
	if err := svc.Stop(); err != nil {
		return fmt.Errorf("stopping service: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "surfbot daemon stopped")
	return nil
}

func runDaemonRestart(cmd *cobra.Command, _ []string) error {
	_, svc, _, err := buildDaemonService()
	if err != nil {
		return err
	}
	if rerr := svc.Restart(); rerr != nil {
		// Manual fallback for platforms where Restart is unreliable
		// (older systemd, certain launchd configs).
		_ = svc.Stop()
		time.Sleep(500 * time.Millisecond)
		if serr := svc.Start(); serr != nil {
			return fmt.Errorf("restart fallback failed: %w (original: %v)", serr, rerr)
		}
	}
	fmt.Fprintln(cmd.OutOrStdout(), "surfbot daemon restarted")
	return nil
}

// statusOutput is the JSON shape emitted by `daemon status --json`.
type statusOutput struct {
	Status     string    `json:"status"`
	PID        int       `json:"pid"`
	Version    string    `json:"version"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	NextScanAt time.Time `json:"next_scan_at,omitempty"`
	LastScanAt time.Time `json:"last_scan_at,omitempty"`
	LogFile    string    `json:"log_file"`
}

func runDaemonStatus(cmd *cobra.Command, _ []string) error {
	mode, err := resolveMode()
	if err != nil {
		return err
	}
	paths := daemon.Resolve(daemon.Default(mode))
	store := daemon.NewStateStore(paths.StateFile())
	st, err := store.Load()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	// Best-effort kardianos status query. We tolerate failures here so
	// `daemon status` still works on a fresh install where the state file
	// exists but kardianos backend can't talk to the service manager.
	statusStr := "unknown"
	exitCode := 4
	cfg := daemon.Config{Mode: mode, Paths: paths, Version: Version}
	if _, svc, berr := daemon.Build(cfg); berr == nil {
		if s, serr := svc.Status(); serr == nil {
			switch s {
			case service.StatusRunning:
				statusStr = "running"
				exitCode = 0
			case service.StatusStopped:
				statusStr = "stopped"
				exitCode = 3
			}
		}
	}

	out := statusOutput{
		Status:     statusStr,
		PID:        st.PID,
		Version:    st.Version,
		StartedAt:  st.StartedAt,
		NextScanAt: st.NextScanAt,
		LastScanAt: st.LastScanAt,
		LogFile:    paths.LogFile(),
	}

	w := cmd.OutOrStdout()
	if daemonStatusJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(w, "surfbot daemon")
		fmt.Fprintf(w, "  status:    %s\n", out.Status)
		fmt.Fprintf(w, "  pid:       %d\n", out.PID)
		fmt.Fprintf(w, "  version:   %s\n", out.Version)
		if !out.StartedAt.IsZero() {
			fmt.Fprintf(w, "  uptime:    %s\n", time.Since(out.StartedAt).Round(time.Second))
		}
		if !out.LastScanAt.IsZero() {
			fmt.Fprintf(w, "  last scan: %s (%s)\n", out.LastScanAt.Format(time.RFC3339), st.LastScanStatus)
		}
		if !out.NextScanAt.IsZero() {
			fmt.Fprintf(w, "  next scan: %s\n", out.NextScanAt.Format(time.RFC3339))
		}
		fmt.Fprintf(w, "  log file:  %s\n", out.LogFile)
	}

	if exitCode != 0 {
		// Use os.Exit so cobra doesn't print the usual error footer for
		// what is really an informational status.
		os.Exit(exitCode)
	}
	return nil
}

func runDaemonLogs(cmd *cobra.Command, _ []string) error {
	mode, err := resolveMode()
	if err != nil {
		return err
	}
	paths := daemon.Resolve(daemon.Default(mode))
	logger := daemon.NewLogger(paths.LogFile(), daemon.LoggerOptions{})
	defer logger.Close() //nolint:errcheck

	w := cmd.OutOrStdout()

	lines, err := logger.Tail(200)
	if err != nil {
		return fmt.Errorf("reading log file: %w", err)
	}
	if daemonLogsSince != "" {
		dur, perr := time.ParseDuration(daemonLogsSince)
		if perr != nil {
			return fmt.Errorf("parsing --since: %w", perr)
		}
		lines = daemon.FilterSince(lines, time.Now().Add(-dur))
	}
	if err := daemon.FormatLines(w, lines); err != nil {
		return err
	}

	if !daemonLogsFollow {
		return nil
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return logger.Follow(ctx, w)
}

// runDaemonRun is the entrypoint the OS service manager invokes. It blocks
// inside service.Run until the service is asked to stop.
func runDaemonRun(_ *cobra.Command, _ []string) error {
	_, svc, _, err := buildDaemonService()
	if err != nil {
		return err
	}
	return svc.Run()
}

// isAlreadyInstalled detects the kardianos error returned when the unit
// file already exists. kardianos doesn't expose a typed error, so we have
// to string-match — but only on a narrow substring set so we don't
// accidentally swallow real failures.
func isAlreadyInstalled(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists") || strings.Contains(msg, "init already")
}

func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "access is denied") ||
		strings.Contains(msg, "must be run as")
}
