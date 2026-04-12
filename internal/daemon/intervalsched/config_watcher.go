package intervalsched

import (
	"context"
	"os"
	"time"
)

// ConfigWatchInterval controls how often the scheduler checks for
// schedule.config.json changes. Overridable in tests.
var ConfigWatchInterval = 5 * time.Second

// WatchConfig starts a background goroutine that polls the config store
// for changes and reloads the scheduler when detected. Exits when ctx
// is canceled.
func (s *IntervalScheduler) WatchConfig(ctx context.Context, store *ScheduleConfigStore) {
	if store == nil {
		return
	}
	var lastMtime time.Time
	if info, err := os.Stat(store.Path()); err == nil {
		lastMtime = info.ModTime()
	}

	t := time.NewTicker(ConfigWatchInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			info, err := os.Stat(store.Path())
			if err != nil {
				continue
			}
			if !info.ModTime().After(lastMtime) {
				continue
			}
			lastMtime = info.ModTime()

			sc, err := store.Load()
			if err != nil {
				s.logger.Warn("config_watcher.load_failed", "err", err)
				continue
			}
			cfg, errs := ParseScheduleConfig(sc)
			if len(errs) > 0 {
				s.logger.Warn("config_watcher.parse_failed", "errors", errs)
				continue
			}
			// Preserve TriggerDir from current config — it's not user-settable.
			s.mu.Lock()
			cfg.TriggerDir = s.cfg.TriggerDir
			s.mu.Unlock()

			s.Reload(cfg)
		}
	}
}

// ParseScheduleConfig converts a ScheduleConfig (JSON strings) into an
// intervalsched.Config and validates it. Returns per-field errors on
// failure.
func ParseScheduleConfig(sc ScheduleConfig) (Config, map[string]string) {
	errs := make(map[string]string)
	var cfg Config

	full, err := time.ParseDuration(sc.FullScanInterval)
	if err != nil {
		errs["full_scan_interval"] = "invalid duration: " + sc.FullScanInterval
	} else {
		cfg.FullInterval = full
	}

	quick, err := time.ParseDuration(sc.QuickCheckInterval)
	if err != nil {
		errs["quick_check_interval"] = "invalid duration: " + sc.QuickCheckInterval
	} else {
		cfg.QuickInterval = quick
	}

	jitter, err := time.ParseDuration(sc.Jitter)
	if err != nil {
		errs["jitter"] = "invalid duration: " + sc.Jitter
	} else {
		cfg.Jitter = jitter
	}

	if len(errs) > 0 {
		return cfg, errs
	}

	cfg.QuickTools = sc.QuickCheckTools
	cfg.RunOnStart = sc.RunOnStart

	mw := MaintenanceWindow{Enabled: sc.MaintenanceWindow.Enabled}
	if mw.Enabled {
		start, serr := ParseTimeOfDay(sc.MaintenanceWindow.Start)
		if serr != nil {
			errs["maintenance_window.start"] = serr.Error()
			return cfg, errs
		}
		end, eerr := ParseTimeOfDay(sc.MaintenanceWindow.End)
		if eerr != nil {
			errs["maintenance_window.end"] = eerr.Error()
			return cfg, errs
		}
		loc := time.Local
		if sc.MaintenanceWindow.Timezone != "" {
			loc, err = time.LoadLocation(sc.MaintenanceWindow.Timezone)
			if err != nil {
				errs["maintenance_window.timezone"] = err.Error()
				return cfg, errs
			}
		}
		mw.Start, mw.End, mw.Loc = start, end, loc
	}
	cfg.Window = mw

	if _, verr := cfg.Validate(); verr != nil {
		errs["_"] = verr.Error()
		return cfg, errs
	}

	return cfg, nil
}
