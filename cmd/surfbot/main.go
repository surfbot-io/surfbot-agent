package main

import "github.com/surfbot-io/surfbot-agent/internal/cli"

// Version info — injected via ldflags at build time.
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	cli.Version = version
	cli.Commit = commit
	cli.BuildDate = buildDate
	cli.Execute()
}
