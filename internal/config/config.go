package config

import (
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	DBPath   string       `yaml:"db_path" mapstructure:"db_path"`
	LogLevel string       `yaml:"log_level" mapstructure:"log_level"`
	Tools    ToolsConfig  `yaml:"tools" mapstructure:"tools"`
	Scan     ScanConfig   `yaml:"scan" mapstructure:"scan"`
	Daemon   DaemonConfig `yaml:"daemon" mapstructure:"daemon"`
}

// DaemonConfig holds settings for `surfbot daemon`. Only ShutdownGrace and
// StateHeartbeat are read in SPEC-X1; the scheduling fields are stubbed
// here so config files written today remain valid once SPEC-X2 lands.
type DaemonConfig struct {
	FullScanInterval   time.Duration `yaml:"full_scan_interval" mapstructure:"full_scan_interval"`
	QuickCheckInterval time.Duration `yaml:"quick_check_interval" mapstructure:"quick_check_interval"`
	ShutdownGrace      time.Duration `yaml:"shutdown_grace" mapstructure:"shutdown_grace"`
	StateHeartbeat     time.Duration `yaml:"state_heartbeat" mapstructure:"state_heartbeat"`
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

	if err := viper.Unmarshal(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
