package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/config"
	"github.com/surfbot-io/surfbot-agent/internal/daemon"
	"github.com/surfbot-io/surfbot-agent/internal/daemon/intervalsched"
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "View and configure scan schedule",
}

var scheduleShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display the current schedule configuration",
	RunE:  runScheduleShow,
}

var scheduleSetCmd = &cobra.Command{
	Use:   "set [key] [value]",
	Short: "Update a schedule setting",
	Long: `Update a schedule setting. Available keys:

  enabled              true/false
  full_scan_interval   Duration (e.g. 24h, 12h)
  quick_check_interval Duration (e.g. 1h, 30m)
  jitter               Duration (e.g. 5m, 10m)
  run_on_start         true/false
  window_enabled       true/false
  window_start         Time (HH:MM, e.g. 02:00)
  window_end           Time (HH:MM, e.g. 04:00)
  window_timezone      IANA timezone (e.g. UTC, Europe/Madrid)

Examples:
  surfbot schedule set full_scan_interval 12h
  surfbot schedule set enabled true
  surfbot schedule set window_start 03:00`,
	Args: cobra.ExactArgs(2),
	RunE: runScheduleSet,
}

func init() {
	scheduleCmd.AddCommand(scheduleShowCmd)
	scheduleCmd.AddCommand(scheduleSetCmd)
	rootCmd.AddCommand(scheduleCmd)
}

func resolveScheduleConfigStore() *intervalsched.ScheduleConfigStore {
	mode := daemon.DefaultMode()
	paths := daemon.Resolve(daemon.Default(mode))
	return intervalsched.NewScheduleConfigStore(scheduleConfigPath(paths))
}

// loadScheduleConfig returns the current schedule config, merging
// persisted schedule.config.json over config.yaml defaults.
func loadScheduleConfig() intervalsched.ScheduleConfig {
	sc := intervalsched.ScheduleConfig{
		FullScanInterval:   "24h",
		QuickCheckInterval: "1h",
		Jitter:             "5m",
	}

	// Read from config.yaml if available.
	cfg, err := config.Load(cfgFile)
	if err == nil {
		sc.Enabled = cfg.Daemon.Scheduler.Enabled
		mw := cfg.Daemon.Scheduler.MaintenanceWindow
		sc.MaintenanceWindow.Start = mw.Start
		sc.MaintenanceWindow.End = mw.End
		sc.MaintenanceWindow.Timezone = mw.Timezone
	}

	// Override with persisted schedule.config.json if it exists.
	store := resolveScheduleConfigStore()
	if store.Exists() {
		persisted, serr := store.Load()
		if serr == nil {
			return persisted
		}
	}

	return sc
}

func runScheduleShow(cmd *cobra.Command, _ []string) error {
	sc := loadScheduleConfig()

	if jsonOut {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(sc)
	}

	p := NewPrinter(cmd.OutOrStdout())
	p.SectionHeader("Schedule Configuration")
	p.Keyf("Enabled", "%v", sc.Enabled)
	p.Keyf("Full scan interval", "%s", sc.FullScanInterval)
	p.Keyf("Quick check interval", "%s", sc.QuickCheckInterval)
	p.Keyf("Jitter", "%s", sc.Jitter)
	p.Keyf("Run on start", "%v", sc.RunOnStart)

	if len(sc.QuickCheckTools) > 0 {
		p.Keyf("Quick check tools", "%v", sc.QuickCheckTools)
	}

	p.Divider(40)
	mw := sc.MaintenanceWindow
	p.Keyf("Window enabled", "%v", mw.Enabled)
	if mw.Start != "" {
		p.Keyf("Window start", "%s", mw.Start)
	}
	if mw.End != "" {
		p.Keyf("Window end", "%s", mw.End)
	}
	if mw.Timezone != "" {
		p.Keyf("Window timezone", "%s", mw.Timezone)
	}

	// Show the config file path.
	store := resolveScheduleConfigStore()
	if store.Exists() {
		p.Divider(40)
		p.Muted("Config: %s", store.Path())
	}

	return nil
}

func runScheduleSet(cmd *cobra.Command, args []string) error {
	key, value := args[0], args[1]
	sc := loadScheduleConfig()

	switch key {
	case "enabled":
		sc.Enabled = parseBool(value)
	case "full_scan_interval":
		sc.FullScanInterval = value
	case "quick_check_interval":
		sc.QuickCheckInterval = value
	case "jitter":
		sc.Jitter = value
	case "run_on_start":
		sc.RunOnStart = parseBool(value)
	case "window_enabled":
		sc.MaintenanceWindow.Enabled = parseBool(value)
	case "window_start":
		sc.MaintenanceWindow.Start = value
	case "window_end":
		sc.MaintenanceWindow.End = value
	case "window_timezone":
		sc.MaintenanceWindow.Timezone = value
	default:
		cmd.SilenceUsage = true
		return fmt.Errorf("unknown key: %s", key)
	}

	// Validate the whole config before saving.
	_, fieldErrors := intervalsched.ParseScheduleConfig(sc)
	if len(fieldErrors) > 0 {
		cmd.SilenceUsage = true
		for field, msg := range fieldErrors {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", field, msg)
		}
		return fmt.Errorf("invalid schedule config")
	}

	store := resolveScheduleConfigStore()
	if err := store.Save(sc); err != nil {
		return fmt.Errorf("saving schedule config: %w", err)
	}

	p := NewPrinter(cmd.OutOrStdout())
	p.Success("Updated %s = %s", key, value)
	p.Muted("Saved to %s", store.Path())
	p.Muted("The daemon will pick up changes within ~5 seconds.")
	return nil
}

func parseBool(s string) bool {
	return s == "true" || s == "1" || s == "yes"
}
