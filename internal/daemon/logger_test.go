package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLogger_Rotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")

	// 1 MB rotation threshold so we don't have to write 10 MB in a test.
	logger := NewLogger(path, LoggerOptions{MaxSizeMB: 1, MaxBackups: 2})
	defer logger.Close() //nolint:errcheck

	// Write ~1.5 MB of log entries.
	big := strings.Repeat("x", 1024)
	for i := 0; i < 1500; i++ {
		logger.Slog().Info("rotate test", "i", i, "data", big)
	}
	require.NoError(t, logger.Close())

	// At least one rotated file must exist alongside the live one.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var rotated int
	for _, e := range entries {
		if e.Name() != "daemon.log" && strings.HasPrefix(e.Name(), "daemon-") {
			rotated++
		}
	}
	require.GreaterOrEqual(t, rotated, 1, "expected at least one rotated log file")
}

func TestLogger_Redaction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "redact.log")
	logger := NewLogger(path, LoggerOptions{MaxSizeMB: 1})

	logger.Slog().Info("creds",
		"token", "tok-deadbeef",
		"api_key", "key-secret",
		"username", "alice",
	)
	require.NoError(t, logger.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(data)

	require.Contains(t, body, "***")
	require.NotContains(t, body, "tok-deadbeef")
	require.NotContains(t, body, "key-secret")
	require.Contains(t, body, "alice") // non-sensitive fields are preserved
}

func TestLogger_TailMissing(t *testing.T) {
	logger := NewLogger(filepath.Join(t.TempDir(), "missing.log"), LoggerOptions{})
	defer logger.Close() //nolint:errcheck
	lines, err := logger.Tail(10)
	require.NoError(t, err)
	require.Empty(t, lines)
}

func TestFilterSince(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-2 * time.Hour).Format(time.RFC3339Nano)
	recent := now.Add(-1 * time.Minute).Format(time.RFC3339Nano)
	lines := []string{
		`{"time":"` + old + `","msg":"old"}`,
		`{"time":"` + recent + `","msg":"recent"}`,
		`not json`, // passes through unchanged
	}
	got := FilterSince(lines, now.Add(-30*time.Minute))
	require.Len(t, got, 2)
	require.Contains(t, got[0], "recent")
	require.Equal(t, "not json", got[1])
}

func TestFormatLines(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, FormatLines(&buf, []string{"a", "b"}))
	require.Equal(t, "a\nb\n", buf.String())
}
