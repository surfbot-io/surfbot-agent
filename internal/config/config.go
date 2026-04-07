package config

import (
	"os"
	"path/filepath"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

type Config struct {
	DBPath   string       `yaml:"db_path" mapstructure:"db_path"`
	LogLevel string       `yaml:"log_level" mapstructure:"log_level"`
	Tools    ToolsConfig  `yaml:"tools" mapstructure:"tools"`
	Scan     ScanConfig   `yaml:"scan" mapstructure:"scan"`
	Daemon   DaemonConfig `yaml:"daemon" mapstructure:"daemon"`
}

// DaemonConfig holds settings for `surfbot daemon`. ShutdownGrace and
// StateHeartbeat are runtime knobs from SPEC-X1; Scheduler is the
// SPEC-X2 scan-cadence configuration.
type DaemonConfig struct {
	ShutdownGrace  time.Duration   `yaml:"shutdown_grace" mapstructure:"shutdown_grace"`
	StateHeartbeat time.Duration   `yaml:"state_heartbeat" mapstructure:"state_heartbeat"`
	Scheduler      SchedulerConfig `yaml:"scheduler" mapstructure:"scheduler"`
}

// SchedulerConfig drives the IntervalScheduler. The daemon refuses to
// start with invalid values; see internal/daemon/intervalsched.Config.Validate.
type SchedulerConfig struct {
	Enabled            bool                    `yaml:"enabled" mapstructure:"enabled"`
	FullScanInterval   time.Duration           `yaml:"full_scan_interval" mapstructure:"full_scan_interval"`
	QuickCheckInterval time.Duration           `yaml:"quick_check_interval" mapstructure:"quick_check_interval"`
	Jitter             time.Duration           `yaml:"jitter" mapstructure:"jitter"`
	MaintenanceWindow  MaintenanceWindowConfig `yaml:"maintenance_window" mapstructure:"maintenance_window"`
	QuickCheckTools    []string                `yaml:"quick_check_tools" mapstructure:"quick_check_tools"`
	RunOnStart         bool                    `yaml:"run_on_start" mapstructure:"run_on_start"`
}

// MaintenanceWindowConfig is the YAML representation of a maintenance
// window. Strings are parsed at scheduler build time so config files
// remain trivially editable.
type MaintenanceWindowConfig struct {
	Enabled  bool   `yaml:"enabled" mapstructure:"enabled"`
	Start    string `yaml:"start" mapstructure:"start"`       // "HH:MM"
	End      string `yaml:"end" mapstructure:"end"`           // "HH:MM"
	Timezone string `yaml:"timezone" mapstructure:"timezone"` // IANA tz
}

type ToolsConfig struct {
	Enabled  []string `yaml:"enabled" mapstructure:"enabled"`
	Disabled []string `yaml:"disabled" mapstructure:"disabled"`
}

type ScanConfig struct {
	DefaultType string `yaml:"default_type" mapstructure:"default_type"`
	RateLimit   int    `yaml:"rate_limit" mapstructure:"rate_limit"`
	Timeout     int    `yaml:"timeout" mapstructure:"timeout"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		DBPath:   filepath.Join(home, ".surfbot", "surfbot.db"),
		LogLevel: "info",
		Tools: ToolsConfig{
			Enabled: []string{"subfinder", "dnsx", "naabu", "httpx", "nuclei"},
		},
		Scan: ScanConfig{
			DefaultType: "full",
			RateLimit:   0,
			Timeout:     300,
		},
		Daemon: DaemonConfig{
			ShutdownGrace:  20 * time.Second,
			StateHeartbeat: 30 * time.Second,
			Scheduler: SchedulerConfig{
				Enabled:            true,
				FullScanInterval:   24 * time.Hour,
				QuickCheckInterval: 1 * time.Hour,
				Jitter:             5 * time.Minute,
				QuickCheckTools:    []string{"httpx", "nuclei"},
				RunOnStart:         false,
			},
		},
	}
}

// Load reads config from the given file path and merges env vars.
func Load(cfgFile string) (*Config, error) {
	cfg := DefaultConfig()

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, _ := os.UserHomeDir()
		viper.AddConfigPath(filepath.Join(home, ".surfbot"))
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	viper.SetEnvPrefix("SURFBOT")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		// Config file not found is not an error — use defaults
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			if !os.IsNotExist(err) {
				return nil, err
			}
		}
	}

	// Hook so YAML strings like "24h" decode into time.Duration fields,
	// and comma-separated env strings decode into []string slices.
	hook := viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	))
	if err := viper.Unmarshal(cfg, hook); err != nil {
		return nil, err
	}

	return cfg, nil
}
