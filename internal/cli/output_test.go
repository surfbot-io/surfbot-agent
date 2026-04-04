package cli

import (
	"bytes"
	"strings"
	"testing"

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

func TestDivider(t *testing.T) {
	color.NoColor = true
	defer func() { color.NoColor = false }()

	var buf bytes.Buffer
	p := NewPrinter(&buf)
	p.Divider(20)
	output := buf.String()
	assert.Contains(t, output, strings.Repeat("─", 20))
}
