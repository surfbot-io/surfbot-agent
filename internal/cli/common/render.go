package common

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// OutputFormat enumerates the `-o` flag's accepted values.
type OutputFormat string

const (
	FormatTable OutputFormat = "table"
	FormatJSON  OutputFormat = "json"
	FormatYAML  OutputFormat = "yaml"
)

// ParseOutputFormat normalizes user input for the `-o` flag. Empty
// string falls back to table. Invalid values return an error so the
// command can exit 2.
func ParseOutputFormat(s string) (OutputFormat, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "table", "text":
		return FormatTable, nil
	case "json":
		return FormatJSON, nil
	case "yaml", "yml":
		return FormatYAML, nil
	default:
		return "", fmt.Errorf("unknown output format %q (expected table|json|yaml)", s)
	}
}

// Render dispatches on format, emitting to w. For table output the
// caller supplies the row-rendering func since each resource has
// different columns; JSON and YAML marshal `v` directly.
func Render(w io.Writer, format OutputFormat, v any, table func(io.Writer) error) error {
	switch format {
	case FormatJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	case FormatYAML:
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		if err := enc.Encode(v); err != nil {
			return err
		}
		return enc.Close()
	default:
		if table == nil {
			// Fall back to JSON if the caller didn't supply a table
			// renderer — better than silent empty output.
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			return enc.Encode(v)
		}
		return table(w)
	}
}

// NewTable returns a tabwriter configured for CLI output. Matches the
// existing cli.Printer.NewTable styling so output across commands
// looks consistent.
func NewTable(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
}

// TerminalWidth returns the current terminal's column count. Falls
// back to 120 when the output isn't a TTY (CI pipes) so table columns
// don't squish unexpectedly.
func TerminalWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 120
	}
	return w
}

// Ellipsize truncates s to maxRunes, replacing the tail with "…"
// when truncation happens. Returns the original when already short
// enough. Runes (not bytes) so UTF-8 boundaries are respected.
func Ellipsize(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes == 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}

// FormatTime formats t for human output. Zero time returns "—" so
// empty cells look deliberate rather than "0001-01-01...".
func FormatTime(t interface{ IsZero() bool; String() string }) string {
	if t.IsZero() {
		return "—"
	}
	return t.String()
}
