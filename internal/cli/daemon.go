package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/config"
	"github.com/surfbot-io/surfbot-agent/internal/daemon"
	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/detection"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
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

// buildDaemonService is the common setup install/uninstall/start/stop/
// status/restart use. It does NOT load surfbot config or open the SQLite
// store — only `daemon run` needs those, and it uses buildDaemonRunService.
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

// buildDaemonRunService is invoked by `daemon run`. It delegates the
// config-load / store-open / legacy-migration / scheduler-construction
// sequence to BuildSchedulerBootstrap (shared with `surfbot ui` under
// SPEC-SCHED2.0) and then wraps the result in a kardianos service.
// The returned bootstrap exposes the store so callers can take the
// scheduler_lock; the cleanup func closes the database and must be
// deferred by the caller.
func buildDaemonRunService() (*daemon.Service, service.Service, *SchedulerBootstrap, error) {
	mode, err := resolveMode()
	if err != nil {
		return nil, nil, nil, err
	}
	boot, err := BuildSchedulerBootstrap(mode)
	if err != nil {
		return nil, nil, nil, err
	}

	dcfg := daemon.Config{
		Mode:           mode,
		Paths:          boot.Paths,
		Version:        Version,
		ShutdownGrace:  durationOr(boot.Config.Daemon.ShutdownGrace, 20*time.Second),
		Heartbeat:      durationOr(boot.Config.Daemon.StateHeartbeat, 30*time.Second),
		ConfigOverride: cfgFile,
		Scheduler:      boot.Scheduler,
	}
	s, svc, err := daemon.Build(dcfg)
	if err != nil {
		boot.Cleanup()
		return nil, nil, nil, err
	}
	return s, svc, boot, nil
}

func durationOr(d, fallback time.Duration) time.Duration {
	if d <= 0 {
		return fallback
	}
	return d
}

// buildSchedulerConcrete constructs the SCHED1.2b master ticker and
// returns it as the concrete *intervalsched.Scheduler so callers that
// need the DispatchAdHoc surface (webui AdHocDispatcher) can use it
// directly without a type assertion. The value still satisfies
// daemon.Scheduler so the Runner accepts it unchanged.
//
// config.SchedulerConfig.Enabled is no longer consulted: with first-class
// schedules the daemon always runs the master ticker. An empty
// scan_schedules table simply produces an idle tick loop.
func buildSchedulerConcrete(_ config.SchedulerConfig, _ daemon.Paths, store *storage.SQLiteStore) (*intervalsched.Scheduler, error) {
	registry := detection.NewRegistry()
	runner := daemon.NewScanRunner(store, registry, slog.Default())

	sched, err := intervalsched.New(intervalsched.Dependencies{
		SchedStore:    store.Schedules(),
		TmplStore:     store.Templates(),
		BlackoutStore: store.Blackouts(),
		DefaultsStore: store.ScheduleDefaults(),
		AdHocStore:    store.AdHocScanRuns(),
		Runner:        runner,
		Log:           slog.Default(),
		Clock:         intervalsched.NewRealClock(),
		TickInterval:  intervalsched.DefaultTickInterval,
	})
	if err != nil {
		return nil, err
	}
	return sched, nil
}

func buildWindow(mw config.MaintenanceWindowConfig) (intervalsched.MaintenanceWindow, error) {
	w := intervalsched.MaintenanceWindow{Enabled: mw.Enabled}
	if !mw.Enabled {
		return w, nil
	}
	start, err := intervalsched.ParseTimeOfDay(mw.Start)
	if err != nil {
		return w, err
	}
	end, err := intervalsched.ParseTimeOfDay(mw.End)
	if err != nil {
		return w, err
	}
	loc := time.Local
	if mw.Timezone != "" {
		loc, err = time.LoadLocation(mw.Timezone)
		if err != nil {
			return w, fmt.Errorf("invalid maintenance_window.timezone: %w", err)
		}
	}
	w.Start, w.End, w.Loc = start, end, loc
	return w, nil
}

// loadSchedulerStatus reads schedule.state.json (if present) and translates
// it into the schedulerStatus shape used by `daemon status`. Returns nil
// when the state file does not exist yet — the daemon either has scheduling
// disabled or has not completed a tick.
func loadSchedulerStatus(paths daemon.Paths) *schedulerStatus {
	store := intervalsched.NewScheduleStateStore(scheduleStatePath(paths))
	st, err := store.Load()
	if err != nil {
		return nil
	}
	if st.LastFullAt.IsZero() && st.LastQuickAt.IsZero() &&
		st.NextFullAt.IsZero() && st.NextQuickAt.IsZero() {
		return nil
	}
	out := &schedulerStatus{
		Enabled:         true,
		LastFullAt:      st.LastFullAt,
		LastFullStatus:  st.LastFullStatus,
		LastQuickAt:     st.LastQuickAt,
		LastQuickStatus: st.LastQuickStatus,
		NextFullAt:      st.NextFullAt,
		NextQuickAt:     st.NextQuickAt,
	}
	if cfg, cerr := config.Load(cfgFile); cerr == nil {
		mw := cfg.Daemon.Scheduler.MaintenanceWindow
		if mw.Enabled {
			out.WindowEnabled = true
			out.WindowDesc = fmt.Sprintf("%s-%s %s", mw.Start, mw.End, mw.Timezone)
			if w, werr := buildWindow(mw); werr == nil {
				if w.Contains(time.Now()) {
					out.WindowState = "closed"
				} else {
					out.WindowState = "open"
				}
			}
		}
	}
	return out
}

func printSchedulerStatus(w io.Writer, s *schedulerStatus) {
	_, _ = fmt.Fprintln(w, "  scheduler:")
	if !s.LastFullAt.IsZero() {
		_, _ = fmt.Fprintf(w, "    last full:  %s (%s)\n", s.LastFullAt.Format(time.RFC3339), s.LastFullStatus)
	}
	if !s.LastQuickAt.IsZero() {
		_, _ = fmt.Fprintf(w, "    last quick: %s (%s)\n", s.LastQuickAt.Format(time.RFC3339), s.LastQuickStatus)
	}
	if !s.NextFullAt.IsZero() {
		_, _ = fmt.Fprintf(w, "    next full:  %s\n", s.NextFullAt.Format(time.RFC3339))
	}
	if !s.NextQuickAt.IsZero() {
		_, _ = fmt.Fprintf(w, "    next quick: %s\n", s.NextQuickAt.Format(time.RFC3339))
	}
	if s.WindowEnabled {
		_, _ = fmt.Fprintf(w, "    window:     %s [%s]\n", s.WindowDesc, s.WindowState)
	}
}

// scheduleStatePath returns the path of schedule.state.json next to
// daemon.state.json so users can inspect/back up both files together.
func scheduleStatePath(paths daemon.Paths) string {
	dir := paths.StateDir
	return dir + string(os.PathSeparator) + "schedule.state.json"
}

// scheduleConfigPath returns the path of schedule.config.json next to
// daemon.state.json. This file takes precedence over config.yaml for
// scheduler settings once the user edits via UI or CLI.
func scheduleConfigPath(paths daemon.Paths) string {
	dir := paths.StateDir
	return dir + string(os.PathSeparator) + "schedule.config.json"
}

func runDaemonInstall(cmd *cobra.Command, _ []string) error {
	_, svc, mode, err := buildDaemonService()
	if err != nil {
		return err
	}
	serviceFile := daemon.ServiceFile(daemon.Default(mode))
	if err := svc.Install(); err != nil {
		// Idempotent: kardianos returns "Init already exists" or similar.
		if isAlreadyInstalled(err) {
			p := NewPrinter(cmd.OutOrStdout())
			p.Info("surfbot daemon already installed")
			if serviceFile != "" {
				p.Keyf("service file", "%s", serviceFile)
			}
			return nil
		}
		if isPermissionError(err) {
			return fmt.Errorf("daemon install requires root/Administrator privileges. Try: sudo surfbot daemon install (original error: %w)", err)
		}
		return fmt.Errorf("installing service: %w", err)
	}
	p := NewPrinter(cmd.OutOrStdout())
	p.Success("surfbot daemon installed (%s mode)", mode)
	if serviceFile != "" {
		p.Keyf("service file", "%s", serviceFile)
	}
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
	NewPrinter(cmd.OutOrStdout()).Success("surfbot daemon uninstalled")
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
	NewPrinter(cmd.OutOrStdout()).Success("surfbot daemon started")
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
	NewPrinter(cmd.OutOrStdout()).Success("surfbot daemon stopped")
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
	NewPrinter(cmd.OutOrStdout()).Success("surfbot daemon restarted")
	return nil
}

// statusOutput is the JSON shape emitted by `daemon status --json`. The
// scheduler block is populated from schedule.state.json (SPEC-X2).
type statusOutput struct {
	Status     string           `json:"status"`
	PID        int              `json:"pid"`
	Version    string           `json:"version"`
	StartedAt  time.Time        `json:"started_at,omitempty"`
	NextScanAt time.Time        `json:"next_scan_at,omitempty"`
	LastScanAt time.Time        `json:"last_scan_at,omitempty"`
	LogFile    string           `json:"log_file"`
	Scheduler  *schedulerStatus `json:"scheduler,omitempty"`
}

type schedulerStatus struct {
	Enabled         bool      `json:"enabled"`
	WindowEnabled   bool      `json:"window_enabled"`
	WindowDesc      string    `json:"window,omitempty"`
	WindowState     string    `json:"window_state,omitempty"` // "open" | "closed"
	LastFullAt      time.Time `json:"last_full_at,omitempty"`
	LastFullStatus  string    `json:"last_full_status,omitempty"`
	LastQuickAt     time.Time `json:"last_quick_at,omitempty"`
	LastQuickStatus string    `json:"last_quick_status,omitempty"`
	NextFullAt      time.Time `json:"next_full_at,omitempty"`
	NextQuickAt     time.Time `json:"next_quick_at,omitempty"`
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
		Scheduler:  loadSchedulerStatus(paths),
	}

	w := cmd.OutOrStdout()
	if daemonStatusJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
	} else {
		_, _ = fmt.Fprintln(w, "surfbot daemon")
		_, _ = fmt.Fprintf(w, "  status:    %s\n", out.Status)
		_, _ = fmt.Fprintf(w, "  pid:       %d\n", out.PID)
		_, _ = fmt.Fprintf(w, "  version:   %s\n", out.Version)
		if !out.StartedAt.IsZero() {
			_, _ = fmt.Fprintf(w, "  uptime:    %s\n", time.Since(out.StartedAt).Round(time.Second))
		}
		if !out.LastScanAt.IsZero() {
			_, _ = fmt.Fprintf(w, "  last scan: %s (%s)\n", out.LastScanAt.Format(time.RFC3339), st.LastScanStatus)
		}
		if !out.NextScanAt.IsZero() {
			_, _ = fmt.Fprintf(w, "  next scan: %s\n", out.NextScanAt.Format(time.RFC3339))
		}
		if out.Scheduler != nil {
			printSchedulerStatus(w, out.Scheduler)
		}
		_, _ = fmt.Fprintf(w, "  log file:  %s\n", out.LogFile)
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

// runDaemonRun is the entrypoint the OS service manager invokes. It loads
// the surfbot config, opens the database, takes the SPEC-SCHED2.0
// scheduler_lock, builds the master ticker, and blocks inside
// service.Run until the service is asked to stop.
func runDaemonRun(_ *cobra.Command, _ []string) error {
	_, svc, boot, err := buildDaemonRunService()
	if err != nil {
		return err
	}
	defer boot.Cleanup()

	// The scheduler lock is taken AFTER the store is open but BEFORE
	// service.Run starts the dispatch loop. If another process already
	// owns the lock, exit non-zero so kardianos surfaces a failed start
	// in `surfbot daemon status`.
	lock, lerr := acquireSchedulerLock(context.Background(), boot.Store)
	if lerr != nil {
		if errors.Is(lerr, storage.ErrLockHeld) {
			return fmt.Errorf("scheduler already running: %w", lerr)
		}
		return fmt.Errorf("acquiring scheduler lock: %w", lerr)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := lock.Close(releaseCtx); err != nil {
			slog.Default().Warn("releasing scheduler_lock", "err", err)
		}
	}()

	// SPEC-SCHED2.1 (SUR-255): reap orphaned scans from a previous
	// crash before the dispatch loop starts. Lock-then-reap order
	// guarantees only the winning scheduler touches in-flight state.
	report, err := intervalsched.ReapOrphanedScans(
		context.Background(),
		intervalsched.NewZombieReapBackend(boot.Store),
		intervalsched.NewRealClock(),
		slog.Default(),
	)
	if err != nil {
		return fmt.Errorf("zombie reap on startup: %w", err)
	}
	if report.ScansReaped > 0 {
		slog.Default().Info("recovered scans from previous crash",
			"scans", report.ScansReaped,
			"adhoc_runs", report.AdHocRunsReaped,
			"tool_runs", report.ToolRunsReaped,
			"duration_ms", report.Duration.Milliseconds(),
		)
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
