package cli

import (
	"github.com/spf13/cobra"
)

var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version, commit, and build date",
	Run: func(cmd *cobra.Command, args []string) {
		p := NewPrinter(cmd.OutOrStdout())
		p.Keyf("surfbot", "%s", Version)
		p.Keyf("commit", "%s", Commit)
		p.Keyf("built", "%s", BuildDate)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
