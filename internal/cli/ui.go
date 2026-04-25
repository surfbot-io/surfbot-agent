package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/daemon"
	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
	"github.com/surfbot-io/surfbot-agent/internal/webui"
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Launch the web dashboard",
	Long: `Starts a local web server and opens the Surfbot dashboard in your browser.

By default the in-process scheduler runs alongside the HTTP server so
schedules created in the UI actually fire. Pass --no-scheduler to opt
out (useful when a separately-installed 'surfbot daemon' already owns
the scheduling loop).`,
	RunE: runUI,
}

func init() {
	uiCmd.Flags().IntP("port", "p", 8470, "Port to listen on")
	uiCmd.Flags().Bool("no-open", false, "Don't auto-open browser")
	uiCmd.Flags().String("bind", "127.0.0.1", "Address to bind to")
	uiCmd.Flags().Bool("no-scheduler", false, "Do not start the in-process scheduler (default: start it)")
	rootCmd.AddCommand(uiCmd)
}

// buildUIDaemonView resolves the daemon state file paths so the embedded
// UI can render the SPEC-X3.1 Agent card. It is best-effort: errors
// collapse to a partially-populated view that the UI renders as "agent
// not running" rather than failing the whole `surfbot ui` command.
//
// SPEC-SCHED2.0: callers pass the bootstrap's already-loaded Config +
// Paths so we don't reload config.yaml just to populate the view.
func buildUIDaemonView(boot *SchedulerBootstrap) *webui.DaemonView {
	view := &webui.DaemonView{
		DaemonStatePath:   boot.Paths.StateFile(),
		ScheduleStatePath: scheduleStatePath(boot.Paths),
		Heartbeat:         30 * time.Second,
	}
	if boot.Config != nil {
		if boot.Config.Daemon.StateHeartbeat > 0 {
			view.Heartbeat = boot.Config.Daemon.StateHeartbeat
		}
		view.SchedulerEnabled = boot.Config.Daemon.Scheduler.Enabled
		mw := boot.Config.Daemon.Scheduler.MaintenanceWindow
		view.WindowStart = mw.Start
		view.WindowEnd = mw.End
		view.WindowTimezone = mw.Timezone
		if w, werr := buildWindow(mw); werr == nil {
			view.Window = w
		}
	}
	return view
}

func runUI(cmd *cobra.Command, args []string) error {
	port, _ := cmd.Flags().GetInt("port")
	noOpen, _ := cmd.Flags().GetBool("no-open")
	bind, _ := cmd.Flags().GetString("bind")
	noScheduler, _ := cmd.Flags().GetBool("no-scheduler")

	// `surfbot ui` is invoked by an interactive user, never by the OS
	// service manager, so always resolve paths in user mode. The system
	// default of /var/lib/surfbot is not writable without root.
	mode := daemon.ModeUser

	boot, err := BuildSchedulerBootstrap(mode)
	if err != nil {
		return err
	}
	defer boot.Cleanup()

	if err := boot.Paths.EnsureDirs(mode); err != nil {
		return fmt.Errorf("preparing ui state dir: %w", err)
	}
	authToken, err := webui.LoadOrCreateUIToken(boot.Paths.StateDir)
	if err != nil {
		return err
	}

	// SPEC-SCHED2.0 R5: only one process may own the scheduler against
	// a given DB. If the lock is already held (typically by a separately
	// installed `surfbot daemon`), fall back to UI-only mode and tell
	// the operator. Their ad-hoc "Run scan now" buttons will return 503
	// because the dispatcher in this process is the wrong one to wire,
	// but read-only dashboard traffic still works.
	var lock *schedulerLockHandle
	if !noScheduler {
		l, lerr := acquireSchedulerLock(context.Background(), boot.Store)
		if lerr != nil {
			if errors.Is(lerr, storage.ErrLockHeld) {
				slog.Info("scheduler already running elsewhere; starting UI in --no-scheduler mode",
					"holder", lerr.Error())
				noScheduler = true
			} else {
				return fmt.Errorf("acquiring scheduler lock: %w", lerr)
			}
		} else {
			lock = l
		}
	}

	// SPEC-SCHED2.1 (SUR-255): once the scheduler_lock is ours, before
	// the master ticker dispatches anything, reap any scans/tool_runs/
	// ad_hoc_scan_runs left in 'running' from a previous process that
	// crashed (panic, OOM, kill -9, power loss). Idempotent — no-op when
	// there are no orphans. Runs after the lock so two competing UI
	// processes never reap each other's in-flight state.
	if lock != nil {
		report, err := intervalsched.ReapOrphanedScans(
			context.Background(),
			intervalsched.NewZombieReapBackend(boot.Store),
			intervalsched.NewRealClock(),
			slog.Default(),
		)
		if err != nil {
			releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 5*time.Second)
			if rerr := lock.Close(releaseCtx); rerr != nil {
				slog.Warn("releasing scheduler_lock after reap failure", "err", rerr)
			}
			cancelRelease()
			return fmt.Errorf("zombie reap on startup: %w", err)
		}
		if report.ScansReaped > 0 {
			slog.Info("recovered scans from previous crash",
				"scans", report.ScansReaped,
				"adhoc_runs", report.AdHocRunsReaped,
				"tool_runs", report.ToolRunsReaped,
				"duration_ms", report.Duration.Milliseconds(),
			)
		}
	}

	daemonView := buildUIDaemonView(boot)
	if !noScheduler {
		// Wire the master ticker through to /api/v1/scans/ad-hoc so the
		// dashboard's "Run scan now" buttons dispatch in-process. Without
		// this, the handler returns 503 /problems/dispatcher-unreachable.
		daemonView.AdHocDispatcher = boot.Scheduler
	}

	srv, ln, err := webui.NewServer(boot.Store, webui.ServerOptions{
		Bind:      bind,
		Port:      port,
		Version:   Version,
		Registry:  registry,
		Daemon:    daemonView,
		AuthToken: authToken,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://%s:%d", bind, port)
	p := NewPrinter(cmd.OutOrStdout())
	p.Success("Surfbot UI running at %s", url)
	if !noScheduler {
		p.Muted("Scheduler running in-process (pid %d)\n", os.Getpid())
	} else {
		p.Muted("Scheduler NOT started (--no-scheduler); schedules will not fire from this process\n")
	}
	p.Muted("Press Ctrl+C to stop\n")

	if !noOpen {
		go browser.OpenURL(url)
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start the scheduler first so its state-file heartbeat claims the
	// daemon.state.json slot before the HTTP server accepts its first
	// request. That ordering makes /api/daemon/status report running
	// on the dashboard immediately after first load.
	var runner *daemon.Runner
	if !noScheduler {
		logger := daemon.NewLogger(boot.Paths.LogFile(), daemon.LoggerOptions{Compress: true})
		stateStore := daemon.NewStateStore(boot.Paths.StateFile())
		runner = daemon.NewRunner(daemon.RunnerConfig{
			Scheduler: boot.Scheduler,
			State:     stateStore,
			Logger:    logger,
			Heartbeat: durationOr(boot.Config.Daemon.StateHeartbeat, 30*time.Second),
			Version:   Version,
			// Cancel the UI's top-level signal context on panic so the
			// HTTP server begins its own shutdown before the supervisor
			// terminates the process.
			OnSchedulerPanic: stop,
		})
		if err := runner.Start(); err != nil {
			return fmt.Errorf("starting scheduler: %w", err)
		}
		slog.Info("scheduler started in-process",
			"pid", os.Getpid(),
			"next_tick", boot.Scheduler.Next(),
		)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	shutdownGrace := durationOr(boot.Config.Daemon.ShutdownGrace, 30*time.Second)

	releaseLock := func() {
		if lock == nil {
			return
		}
		releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelRelease()
		if err := lock.Close(releaseCtx); err != nil {
			slog.Warn("releasing scheduler_lock", "err", err)
		}
	}

	select {
	case <-ctx.Done():
		p.Muted("\nShutting down...\n")
		shutdownUI(srv, runner, shutdownGrace, p)
		releaseLock()
		return nil
	case err := <-errCh:
		shutdownUI(srv, runner, shutdownGrace, p)
		releaseLock()
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	}
}

// httpShutdownGrace bounds how long srv.Shutdown is allowed to drain
// in-flight HTTP handlers before the UI moves on to stopping the
// scheduler. 10s is plenty for the read-only dashboard traffic and is
// well under the scheduler's own drain budget.
const httpShutdownGrace = 10 * time.Second

// shutdownUI implements the SPEC-SCHED2.0 R4 shutdown sequence:
//  1. drain the HTTP server (bounded by httpShutdownGrace) so no new
//     requests land while the scheduler is being torn down
//  2. stop the scheduler runner (bounded by schedulerGrace, which
//     includes the worker pool drain)
//  3. return so runUI's deferred boot.Cleanup() closes the store
//
// A scheduler timeout logs a structured warning and returns anyway —
// the orphaned in_progress scan rows are SCHED2.1's problem to reap
// on next boot.
func shutdownUI(srv *http.Server, runner *daemon.Runner, schedulerGrace time.Duration, p *Printer) {
	httpCtx, cancel := context.WithTimeout(context.Background(), httpShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(httpCtx); err != nil {
		slog.Warn("http server shutdown did not complete cleanly", "err", err)
		if p != nil {
			p.Muted("http drain exceeded %s; forcing close\n", httpShutdownGrace)
		}
	}
	if runner == nil {
		return
	}
	if err := runner.Stop(schedulerGrace); err != nil {
		slog.Warn("scheduler shutdown timeout",
			"grace", schedulerGrace,
			"err", err,
		)
		if p != nil {
			p.Muted("scheduler did not drain within %s; in-flight scans will be reaped on next boot\n", schedulerGrace)
		}
	}
}
