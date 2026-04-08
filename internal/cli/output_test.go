package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func TestDefaultTheme(t *testing.T) {
	theme := DefaultTheme()
	assert.NotNil(t, theme.Critical)
	assert.NotNil(t, theme.High)
	assert.NotNil(t, theme.Medium)
	assert.NotNil(t, theme.Low)
	assert.NotNil(t, theme.Info)
	assert.NotNil(t, theme.Success)
	assert.NotNil(t, theme.Warning)
	assert.NotNil(t, theme.Error)
	assert.NotNil(t, theme.Progress)
	assert.NotNil(t, theme.Muted)
	assert.NotNil(t, theme.Bold)
	assert.NotNil(t, theme.Header)
}

func TestSeverityColor(t *testing.T) {
	theme := DefaultTheme()
	assert.Equal(t, theme.Critical, theme.SeverityColor(model.SeverityCritical))
	assert.Equal(t, theme.High, theme.SeverityColor(model.SeverityHigh))
	assert.Equal(t, theme.Medium, theme.SeverityColor(model.SeverityMedium))
	assert.Equal(t, theme.Low, theme.SeverityColor(model.SeverityLow))
	assert.Equal(t, theme.Info, theme.SeverityColor(model.SeverityInfo))
	// Unknown severity falls through to Info
	assert.Equal(t, theme.Info, theme.SeverityColor(model.Severity("unknown")))
}

func TestSeverityColorString(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	var buf bytes.Buffer
	p := NewPrinter(&buf)
	sev := p.Severity(model.SeverityCritical)
	assert.Equal(t, "CRITICAL", strings.TrimSpace(sev))
}

func TestNoColorMode(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	var buf bytes.Buffer
	p := NewPrinter(&buf)
	p.Success("test message %d", 42)
	output := buf.String()
	assert.NotContains(t, output, "\x1b[")
	assert.Contains(t, output, "[+]")
	assert.Contains(t, output, "test message 42")
}

func TestProgressMarkers(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	tests := []struct {
		name   string
		call   func(p *Printer)
		prefix string
	}{
		{"progress", func(p *Printer) { p.Progress("msg") }, "[*]"},
		{"success", func(p *Printer) { p.Success("msg") }, "[+]"},
		{"warn", func(p *Printer) { p.Warn("msg") }, "[!]"},
		{"error", func(p *Printer) { p.Errorf("msg") }, "[✗]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			p := NewPrinter(&buf)
			tt.call(p)
			assert.Contains(t, buf.String(), tt.prefix)
			assert.Contains(t, buf.String(), "msg")
		})
	}
}

func TestEmptyState(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	var buf bytes.Buffer
	p := NewPrinter(&buf)
	p.EmptyState("No items found.", "Try running scan first.")
	output := buf.String()
	assert.Contains(t, output, "No items found.")
	assert.Contains(t, output, "Hint: Try running scan first.")
}

func TestEmptyStateNoHint(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	var buf bytes.Buffer
	p := NewPrinter(&buf)
	p.EmptyState("No items found.", "")
	output := buf.String()
	assert.Contains(t, output, "No items found.")
	assert.NotContains(t, output, "Hint:")
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
	assert.Equal(t, "hello w...", truncate("hello world foo", 10))
	assert.Equal(t, "ab", truncate("ab", 5))
	assert.Equal(t, "", truncate("", 5))
}

func TestNewTablePadding(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf)
	w := p.NewTable()
	require.NotNil(t, w)
	// Write two columns and verify padding=3
	w.Write([]byte("A\tB\n"))
	w.Flush()
	output := buf.String()
	// With padding=3, "A" should be followed by at least 3 spaces before "B"
	assert.Contains(t, output, "A   B")
}

func TestInfo(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	var buf bytes.Buffer
	p := NewPrinter(&buf)
	p.Info("hello %s", "world")
	assert.Equal(t, "[i] hello world\n", buf.String())
}

func TestKeyf(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	var buf bytes.Buffer
	p := NewPrinter(&buf)
	p.Keyf("version", "%s", "1.2.3")
	assert.Equal(t, "version: 1.2.3\n", buf.String())
}

func TestBullet(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	var buf bytes.Buffer
	p := NewPrinter(&buf)
	p.Bullet("item %d", 1)
	assert.Equal(t, "  • item 1\n", buf.String())
}

func TestSeverityCount(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	var buf bytes.Buffer
	p := NewPrinter(&buf)
	p.SeverityCount(model.SeverityCritical, 3)
	p.SeverityCount(model.SeverityLow, 0)
	out := buf.String()
	assert.Contains(t, out, "CRITICAL")
	assert.Contains(t, out, "3")
	assert.Contains(t, out, "LOW")
	assert.Contains(t, out, "0")
}

func TestElapsed(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	var buf bytes.Buffer
	p := NewPrinter(&buf)
	got := p.Elapsed(2*time.Minute + 34*time.Second)
	assert.Equal(t, "2m34s", got)
}

func TestActionHint(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	var buf bytes.Buffer
	p := NewPrinter(&buf)
	p.ActionHint("run scan")
	assert.Equal(t, "→ next: run scan\n", buf.String())
}

func TestSeverityColorMap(t *testing.T) {
	theme := DefaultTheme()
	tests := []struct {
		sev  model.Severity
		want *color.Color
	}{
		{model.SeverityCritical, theme.Critical},
		{model.SeverityHigh, theme.High},
		{model.SeverityMedium, theme.Medium},
		{model.SeverityLow, theme.Low},
		{model.SeverityInfo, theme.Info},
	}
	for _, tt := range tests {
		assert.Same(t, tt.want, theme.SeverityColor(tt.sev))
	}
}

// TestThemeUnchanged freezes the v1 theme fingerprint. Any theme color change
// requires deliberately updating this value alongside a roadmap entry.
func TestThemeUnchanged(t *testing.T) {
	got := themeFingerprint(DefaultTheme())
	// Regenerate with -update if the theme is intentionally changed.
	const want = "2691d0954dff6d4528c671b59b6387a1befa8595577aacc78fe5216a56cbb0b3"
	if got != want {
		t.Fatalf("theme fingerprint changed:\n  got:  %s\n  want: %s", got, want)
	}
	// Sanity: fingerprint is stable across calls.
	assert.Equal(t, got, themeFingerprint(DefaultTheme()))
}

func TestDivider(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	var buf bytes.Buffer
	p := NewPrinter(&buf)
	p.Divider(20)
	output := buf.String()
	assert.Contains(t, output, strings.Repeat("─", 20))
}
