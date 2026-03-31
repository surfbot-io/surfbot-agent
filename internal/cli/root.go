package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

var (
	cfgFile string
	dbPath  string
	verbose bool
	jsonOut bool
	noColor bool
)

var store *storage.SQLiteStore

// Commands that don't need database access.
var skipDBCommands = map[string]bool{
	"version":    true,
	"help":       true,
	"completion": true,
}

var rootCmd = &cobra.Command{
	Use:   "surfbot",
	Short: "Surfbot Agent — local security scanner",
	Long:  "Surfbot Agent is a local security scanner with pluggable detection and remediation tools.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if skipDBCommands[cmd.Name()] {
			return nil
		}

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
		// Config file is optional
		viper.ReadInConfig() //nolint:errcheck

		if dbPath != "" {
			viper.Set("db_path", dbPath)
		}

		path := viper.GetString("db_path")
		var err error
		store, err = storage.NewSQLiteStore(path)
		if err != nil {
			return fmt.Errorf("failed to open database: %w", err)
		}
		return nil
	},
	PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
		if store != nil {
			return store.Close()
		}
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Config file (default ~/.surfbot/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", "Database path (default ~/.surfbot/surfbot.db)")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Verbose output")
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "Disable colored output")
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
