package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

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

// Info prints "[i] message" in the default style (no color).
func (p *Printer) Info(format string, args ...interface{}) {
	fmt.Fprintf(p.W, "[i] "+format+"\n", args...)
}

// Keyf prints a key/value pair: "<key>: <value>" with the key dim.
func (p *Printer) Keyf(key, format string, args ...interface{}) {
	p.Theme.Muted.Fprintf(p.W, "%s: ", key)
	fmt.Fprintf(p.W, format+"\n", args...)
}

// Bullet prints a single bullet line: "  • message".
func (p *Printer) Bullet(format string, args ...interface{}) {
	fmt.Fprintf(p.W, "  • "+format+"\n", args...)
}

// SeverityCount prints a severity tally line: "CRITICAL  3" with color.
func (p *Printer) SeverityCount(sev model.Severity, count int) {
	c := p.Theme.SeverityColor(sev)
	label := c.Sprintf("%-8s", strings.ToUpper(string(sev)))
	if count == 0 {
		p.Theme.Muted.Fprintf(p.W, "%s  %d\n", label, count)
		return
	}
	fmt.Fprintf(p.W, "%s  %d\n", label, count)
}

// Elapsed formats a duration as "2m34s" with muted styling.
func (p *Printer) Elapsed(d time.Duration) string {
	d = d.Round(time.Second)
	return p.Theme.Muted.Sprint(d.String())
}

// ActionHint prints a muted "→ next: <hint>" line for empty/completion states.
func (p *Printer) ActionHint(format string, args ...interface{}) {
	p.Theme.Muted.Fprintf(p.W, "→ next: "+format+"\n", args...)
}

// ScoreBar renders a security score as a colored 34-cell bar plus a risk
// band label. Bands: 0–40 red CRITICAL, 41–70 yellow MEDIUM, 71–90 green
// LOW, 91–100 bold green MINIMAL.
func (p *Printer) ScoreBar(score int) {
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	const width = 34
	filled := score * width / 100
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)

	var c *color.Color
	var band string
	switch {
	case score <= 40:
		c, band = p.Theme.Critical, "CRITICAL risk"
	case score <= 70:
		c, band = p.Theme.Medium, "HIGH risk"
	case score <= 90:
		c, band = p.Theme.Success, "LOW risk"
	default:
		c, band = color.New(color.FgGreen, color.Bold), "MINIMAL risk"
	}
	fmt.Fprintf(p.W, "Security score: %d/100\n", score)
	fmt.Fprintf(p.W, "  %s\n", c.Sprint(bar))
	fmt.Fprintf(p.W, "  %s\n", c.Sprint(band))
}

// themeFingerprint returns a stable hash of the theme's color attributes,
// used to detect accidental drift in golden theme tests.
func themeFingerprint(t *Theme) string {
	parts := []string{
		fmt.Sprintf("critical=%v", t.Critical),
		fmt.Sprintf("high=%v", t.High),
		fmt.Sprintf("medium=%v", t.Medium),
		fmt.Sprintf("low=%v", t.Low),
		fmt.Sprintf("info=%v", t.Info),
		fmt.Sprintf("success=%v", t.Success),
		fmt.Sprintf("warning=%v", t.Warning),
		fmt.Sprintf("error=%v", t.Error),
		fmt.Sprintf("progress=%v", t.Progress),
		fmt.Sprintf("muted=%v", t.Muted),
		fmt.Sprintf("bold=%v", t.Bold),
		fmt.Sprintf("header=%v", t.Header),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

// truncate shortens s to max characters, appending "..." if truncated.
func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}
