package intervalsched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// ErrInvalidLegacyInterval is returned when full_scan_interval parses to
// a value the scheduler cannot express as a safe RRULE (sub-minute
// cadence, zero/negative, unparseable).
var ErrInvalidLegacyInterval = errors.New("invalid legacy interval")

// LegacyMigrationBackend is the narrow surface MigrateLegacyScheduleConfig
// needs from the storage layer. It abstracts storage.SQLiteStore so
// tests can inject faults without a real SQLite dependency.
type LegacyMigrationBackend interface {
	Transact(ctx context.Context, fn func(context.Context, storage.TxStores) error) error
	ListTargets(ctx context.Context) ([]model.Target, error)
}

// MigrationReport describes what MigrateLegacyScheduleConfig did or why
// it skipped. Non-empty SkippedReason means no DB writes occurred.
type MigrationReport struct {
	TemplateID       string
	TargetsMigrated  int
	SchedulesCreated int
	SkippedReason    string
}

// MigrateLegacyScheduleConfig is a one-shot promotion of
// <stateDir>/schedule.config.json to a first-class `default` template,
// updated schedule_defaults, and per-target `default` schedules. It is
// idempotent: once it renames the source file to
// schedule.config.json.migrated, subsequent invocations return
// SkippedReason="already_migrated" without touching the database.
//
// NOT YET WIRED into daemon boot — PR SCHED1.2 handles that. This PR
// only compiles and tests the function.
func MigrateLegacyScheduleConfig(
	ctx context.Context,
	stateDir string,
	backend LegacyMigrationBackend,
	logger *slog.Logger,
) (MigrationReport, error) {
	if logger == nil {
		logger = slog.Default()
	}

	sentinel := filepath.Join(stateDir, "schedule.config.json.migrated")
	source := filepath.Join(stateDir, "schedule.config.json")

	if _, err := os.Stat(sentinel); err == nil {
		return MigrationReport{SkippedReason: "already_migrated"}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return MigrationReport{}, fmt.Errorf("stat sentinel: %w", err)
	}

	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return MigrationReport{SkippedReason: "no_legacy_file"}, nil
	} else if err != nil {
		return MigrationReport{}, fmt.Errorf("stat source: %w", err)
	}

	cfg, err := NewScheduleConfigStore(source).Load()
	if err != nil {
		return MigrationReport{}, fmt.Errorf("load legacy config: %w", err)
	}

	full, err := time.ParseDuration(cfg.FullScanInterval)
	if err != nil {
		return MigrationReport{}, fmt.Errorf("parse full_scan_interval %q: %w", cfg.FullScanInterval, err)
	}
	rrule, err := durationToRRule(full)
	if err != nil {
		return MigrationReport{}, err
	}

	targets, err := backend.ListTargets(ctx)
	if err != nil {
		return MigrationReport{}, fmt.Errorf("list targets: %w", err)
	}

	report := MigrationReport{}
	timezone := cfg.MaintenanceWindow.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	maintWin := legacyWindowToMaintenance(cfg.MaintenanceWindow)

	err = backend.Transact(ctx, func(ctx context.Context, stores storage.TxStores) error {
		tmpl := &model.Template{
			Name:        "default",
			Description: "migrated from schedule.config.json",
			RRule:       rrule,
			Timezone:    timezone,
			ToolConfig:  legacyToolsToConfig(cfg.QuickCheckTools),
			IsSystem:    true,
		}
		if maintWin != nil {
			tmpl.MaintenanceWindow = maintWin
		}
		if err := stores.Templates.Create(ctx, tmpl); err != nil {
			return fmt.Errorf("create default template: %w", err)
		}
		report.TemplateID = tmpl.ID

		defaults, err := stores.ScheduleDefaults.Get(ctx)
		if err != nil {
			return fmt.Errorf("get schedule defaults: %w", err)
		}
		defaults.DefaultTemplateID = &tmpl.ID
		defaults.DefaultRRule = rrule
		defaults.DefaultTimezone = timezone
		defaults.RunOnStart = cfg.RunOnStart
		if jitter, err := time.ParseDuration(cfg.Jitter); err == nil && jitter > 0 {
			defaults.JitterSeconds = int(jitter.Seconds())
		}
		if maintWin != nil {
			defaults.DefaultMaintenanceWindow = maintWin
		}
		if err := stores.ScheduleDefaults.Update(ctx, defaults); err != nil {
			return fmt.Errorf("update schedule defaults: %w", err)
		}

		nextRun := computeInitialNextRun(cfg, time.Now().UTC())
		for _, tgt := range targets {
			if !tgt.Enabled {
				continue
			}
			s := &model.Schedule{
				TargetID:   tgt.ID,
				Name:       "default",
				RRule:      rrule,
				DTStart:    time.Now().UTC(),
				Timezone:   timezone,
				TemplateID: &tmpl.ID,
				Overrides:  []string{},
				ToolConfig: model.ToolConfig{},
				Enabled:    true,
				NextRunAt:  nextRun,
			}
			if err := stores.Schedules.Create(ctx, s); err != nil {
				return fmt.Errorf("create schedule for target %s: %w", tgt.ID, err)
			}
			report.TargetsMigrated++
			report.SchedulesCreated++
		}
		return nil
	})
	if err != nil {
		return MigrationReport{}, err
	}

	if err := os.Rename(source, sentinel); err != nil {
		return MigrationReport{}, fmt.Errorf("rename source to sentinel: %w", err)
	}

	logger.Info("migrated schedule.config.json",
		"template", "default",
		"template_id", report.TemplateID,
		"schedules_created", report.SchedulesCreated,
		"targets_migrated", report.TargetsMigrated,
	)
	return report, nil
}

// durationToRRule maps a ScheduleConfig.FullScanInterval duration onto
// an RFC-5545 RRULE. Mappings defined by SPEC-SCHED1 R21 step 4:
//
//	 15m   → FREQ=MINUTELY;INTERVAL=15
//	 1h    → FREQ=HOURLY
//	 6h    → FREQ=HOURLY;INTERVAL=6
//	 24h   → FREQ=DAILY
//	 7d    → FREQ=WEEKLY
//	 30d   → FREQ=MONTHLY
//	 other → FREQ=DAILY (fallback)
//	 <1m   → ErrInvalidLegacyInterval
func durationToRRule(d time.Duration) (string, error) {
	if d < time.Minute {
		return "", fmt.Errorf("%w: %s (min 1m)", ErrInvalidLegacyInterval, d)
	}
	switch {
	case d == 15*time.Minute:
		return "FREQ=MINUTELY;INTERVAL=15", nil
	case d == time.Hour:
		return "FREQ=HOURLY", nil
	case d == 6*time.Hour:
		return "FREQ=HOURLY;INTERVAL=6", nil
	case d == 24*time.Hour:
		return "FREQ=DAILY", nil
	case d == 7*24*time.Hour:
		return "FREQ=WEEKLY", nil
	case d == 30*24*time.Hour:
		return "FREQ=MONTHLY", nil
	default:
		return "FREQ=DAILY", nil
	}
}

// legacyToolsToConfig translates the legacy QuickCheckTools list into a
// ToolConfig. In v1 we only seed the keys — downstream work (SCHED1.2)
// will populate richer params from scan-time context.
func legacyToolsToConfig(tools []string) model.ToolConfig {
	if len(tools) == 0 {
		return model.ToolConfig{}
	}
	tc := model.ToolConfig{}
	for _, name := range tools {
		name = strings.ToLower(strings.TrimSpace(name))
		if _, known := model.RegisteredToolParams[name]; !known {
			// Unknown tools in the legacy config would block ValidateToolConfig;
			// skip them rather than fail the migration. The operator may
			// reintroduce such params on a per-schedule basis later.
			continue
		}
		tc[name] = []byte(`{}`)
	}
	return tc
}

// legacyWindowToMaintenance converts the legacy HH:MM window into a
// nested RRULE-based MaintenanceWindow. A disabled window returns nil.
func legacyWindowToMaintenance(w ScheduleConfigWindow) *model.MaintenanceWindow {
	if !w.Enabled {
		return nil
	}
	tz := w.Timezone
	if tz == "" {
		tz = "UTC"
	}
	start, err := ParseTimeOfDay(w.Start)
	if err != nil {
		return nil
	}
	end, err := ParseTimeOfDay(w.End)
	if err != nil {
		return nil
	}
	startMins := start.Hour*60 + start.Minute
	endMins := end.Hour*60 + end.Minute
	var durationSec int
	if endMins > startMins {
		durationSec = (endMins - startMins) * 60
	} else {
		// Crosses midnight: window is [start, 24h) ∪ [0, end)
		durationSec = ((24*60 - startMins) + endMins) * 60
	}
	return &model.MaintenanceWindow{
		RRule:       fmt.Sprintf("FREQ=DAILY;BYHOUR=%d;BYMINUTE=%d", start.Hour, start.Minute),
		DurationSec: durationSec,
		Timezone:    tz,
	}
}

// computeInitialNextRun picks the schedule's next fire timestamp at
// migration time. Honors RunOnStart: true → now, false → now + 1 tick
// of full_scan_interval. The scheduler re-materializes this on its next
// tick via the RRULE library anyway; this is just a reasonable seed.
func computeInitialNextRun(cfg ScheduleConfig, now time.Time) *time.Time {
	if cfg.RunOnStart {
		t := now
		return &t
	}
	d, err := time.ParseDuration(cfg.FullScanInterval)
	if err != nil || d <= 0 {
		t := now.Add(24 * time.Hour)
		return &t
	}
	t := now.Add(d)
	return &t
}
