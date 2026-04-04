package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/fatih/color"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// Theme holds all color functions for consistent output.
type Theme struct {
	// Severity
	Critical *color.Color
	High     *color.Color
	Medium   *color.Color
	Low      *color.Color
	Info     *color.Color

	// Semantic
	Success  *color.Color
	Warning  *color.Color
	Error    *color.Color
	Progress *color.Color
	Muted    *color.Color
	Bold     *color.Color
	Header   *color.Color
}

// DefaultTheme returns the standard surfbot color theme.
func DefaultTheme() *Theme {
	return &Theme{
		Critical: color.New(color.FgRed, color.Bold),
		High:     color.New(color.FgRed),
		Medium:   color.New(color.FgYellow),
		Low:      color.New(color.FgBlue),
		Info:     color.New(color.Faint),

		Success:  color.RGB(0, 229, 153), // Surfbot Signal Green #00E599
		Warning:  color.New(color.FgYellow),
		Error:    color.New(color.FgRed, color.Bold),
		Progress: color.New(color.FgCyan),
		Muted:    color.New(color.Faint),
		Bold:     color.New(color.Bold),
		Header:   color.New(color.Bold, color.Underline),
	}
}

// SeverityColor returns the color for a given severity level.
func (t *Theme) SeverityColor(sev model.Severity) *color.Color {
	switch sev {
	case model.SeverityCritical:
		return t.Critical
	case model.SeverityHigh:
		return t.High
	case model.SeverityMedium:
		return t.Medium
	case model.SeverityLow:
		return t.Low
	default:
		return t.Info
	}
}

// Printer wraps an io.Writer with themed output methods.
type Printer struct {
	W     io.Writer
	Theme *Theme
}

// NewPrinter creates a Printer writing to w with the default theme.
func NewPrinter(w io.Writer) *Printer {
	return &Printer{W: w, Theme: DefaultTheme()}
}

// Progress prints "[*] message" in cyan.
func (p *Printer) Progress(format string, args ...interface{}) {
	p.Theme.Progress.Fprintf(p.W, "[*] ")
	fmt.Fprintf(p.W, format+"\n", args...)
}

// Success prints "[+] message" in green.
func (p *Printer) Success(format string, args ...interface{}) {
	p.Theme.Success.Fprintf(p.W, "[+] ")
	fmt.Fprintf(p.W, format+"\n", args...)
}

// Warn prints "[!] message" in yellow.
func (p *Printer) Warn(format string, args ...interface{}) {
	p.Theme.Warning.Fprintf(p.W, "[!] ")
	fmt.Fprintf(p.W, format+"\n", args...)
}

// Errorf prints "[✗] message" in red bold.
func (p *Printer) Errorf(format string, args ...interface{}) {
	p.Theme.Error.Fprintf(p.W, "[✗] ")
	fmt.Fprintf(p.W, format+"\n", args...)
}

// Severity returns a colored, padded severity label.
func (p *Printer) Severity(sev model.Severity) string {
	c := p.Theme.SeverityColor(sev)
	return c.Sprintf("%-8s", strings.ToUpper(string(sev)))
}

// SectionHeader prints a bold underlined section header.
func (p *Printer) SectionHeader(text string) {
	p.Theme.Header.Fprintf(p.W, "\n%s\n", text)
}

// Muted prints dimmed text.
func (p *Printer) Muted(format string, args ...interface{}) {
	p.Theme.Muted.Fprintf(p.W, format, args...)
}

// Divider prints a horizontal rule of the given width.
func (p *Printer) Divider(width int) {
	p.Theme.Muted.Fprintln(p.W, strings.Repeat("─", width))
}

// NewTable returns a tabwriter with consistent padding across all commands.
func (p *Printer) NewTable() *tabwriter.Writer {
	return tabwriter.NewWriter(p.W, 0, 0, 3, ' ', 0)
}

// EmptyState prints a helpful message when no data is found.
func (p *Printer) EmptyState(message string, hint string) {
	p.Theme.Muted.Fprintf(p.W, "%s\n", message)
	if hint != "" {
		p.Theme.Muted.Fprintf(p.W, "Hint: %s\n", hint)
	}
}

// truncate shortens s to max characters, appending "..." if truncated.
func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}
