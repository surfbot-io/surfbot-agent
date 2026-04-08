package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/config"
	"github.com/surfbot-io/surfbot-agent/internal/daemon"
	"github.com/surfbot-io/surfbot-agent/internal/webui"
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Launch the web dashboard",
	Long:  "Starts a local web server and opens the Surfbot dashboard in your browser.",
	RunE:  runUI,
}

func init() {
	uiCmd.Flags().IntP("port", "p", 8470, "Port to listen on")
	uiCmd.Flags().Bool("no-open", false, "Don't auto-open browser")
	uiCmd.Flags().String("bind", "127.0.0.1", "Address to bind to")
	rootCmd.AddCommand(uiCmd)
}

// buildUIDaemonView resolves the daemon state file paths and config so the
// embedded UI can render the SPEC-X3.1 Agent card. It is best-effort:
// errors collapse to a partially-populated view that the UI renders as
// "agent not running" rather than failing the whole `surfbot ui` command.
func buildUIDaemonView() *webui.DaemonView {
	mode := daemon.DefaultMode()
	paths := daemon.Resolve(daemon.Default(mode))
	view := &webui.DaemonView{
		DaemonStatePath:   paths.StateFile(),
		ScheduleStatePath: scheduleStatePath(paths),
		Heartbeat:         30 * time.Second,
	}
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return view
	}
	if cfg.Daemon.StateHeartbeat > 0 {
		view.Heartbeat = cfg.Daemon.StateHeartbeat
	}
	view.SchedulerEnabled = cfg.Daemon.Scheduler.Enabled
	mw := cfg.Daemon.Scheduler.MaintenanceWindow
	view.WindowStart = mw.Start
	view.WindowEnd = mw.End
	view.WindowTimezone = mw.Timezone
	if w, werr := buildWindow(mw); werr == nil {
		view.Window = w
	}
	return view
}

func runUI(cmd *cobra.Command, args []string) error {
	port, _ := cmd.Flags().GetInt("port")
	noOpen, _ := cmd.Flags().GetBool("no-open")
	bind, _ := cmd.Flags().GetString("bind")

	srv, ln, err := webui.NewServer(store, webui.ServerOptions{
		Bind:     bind,
		Port:     port,
		Version:  Version,
		Registry: registry,
		Daemon:   buildUIDaemonView(),
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://%s:%d", bind, port)
	fmt.Printf("Surfbot UI running at %s\n", url)
	fmt.Println("Press Ctrl+C to stop")

	if !noOpen {
		go browser.OpenURL(url)
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		// Use Serve(ln) instead of ListenAndServe to avoid TOCTOU race
		errCh <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		fmt.Println("\nShutting down...")
		return srv.Shutdown(context.Background())
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	}
}
