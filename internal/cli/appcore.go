package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/surfbot-io/surfbot-agent/internal/config"
	"github.com/surfbot-io/surfbot-agent/internal/daemon"
	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// SchedulerBootstrap groups everything the in-process scheduler needs.
// The same shape is used by `daemon run` and by `surfbot ui` when it
// auto-starts the scheduler (SPEC-SCHED2.0). Scheduler is the concrete
// *intervalsched.Scheduler so callers can use it both as a
// daemon.Scheduler (for the Runner) and as a webui.AdHocDispatcher
// (for the /api/v1/scans/ad-hoc handler).
type SchedulerBootstrap struct {
	Config    *config.Config
	Store     *storage.SQLiteStore
	Scheduler *intervalsched.Scheduler
	Paths     daemon.Paths
	Mode      daemon.Mode
	// Cleanup closes the store. Callers MUST defer it.
	Cleanup func()
}

// BuildSchedulerBootstrap is the single source of truth for constructing
// the scheduler and its dependencies. It loads the surfbot config, opens
// the SQLite store, runs the idempotent legacy-schedule migration, and
// builds the intervalsched master ticker.
//
// On any failure after the store has been opened the store is closed
// before the error is returned so callers don't leak file handles.
func BuildSchedulerBootstrap(mode daemon.Mode) (*SchedulerBootstrap, error) {
	paths := daemon.Resolve(daemon.Default(mode))

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	runStore, err := storage.NewSQLiteStore(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	cleanup := func() { _ = runStore.Close() }

	report, err := intervalsched.MigrateLegacyScheduleConfig(
		context.Background(), paths.StateDir, runStore, slog.Default())
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("legacy schedule migration: %w", err)
	}
	if report.SkippedReason == "" {
		slog.Default().Info("legacy schedule migrated",
			"template_id", report.TemplateID,
			"targets_migrated", report.TargetsMigrated,
			"schedules_created", report.SchedulesCreated,
		)
	}

	// SCHED2.3: seed the builtin templates (Default / Fast / Deep) so a
	// fresh install's schedule create flow is non-empty out of the box.
	// Idempotent on subsequent boots — operator edits to a builtin
	// survive because we only insert rows whose name is absent.
	// Ordered after legacy migration (which may itself create a
	// historical "default" template) and before the scheduler is
	// constructed so ad-hoc dispatch and the API see a populated
	// catalog from tick zero.
	if _, err := daemon.SeedBuiltinTemplates(
		context.Background(), runStore, slog.Default(),
	); err != nil {
		cleanup()
		return nil, fmt.Errorf("seeding builtin templates: %w", err)
	}

	sched, err := buildSchedulerConcrete(cfg.Daemon.Scheduler, paths, runStore)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("building scheduler: %w", err)
	}

	return &SchedulerBootstrap{
		Config:    cfg,
		Store:     runStore,
		Scheduler: sched,
		Paths:     paths,
		Mode:      mode,
		Cleanup:   cleanup,
	}, nil
}
