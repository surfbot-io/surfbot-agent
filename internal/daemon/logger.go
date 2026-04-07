package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// redactKey matches attribute keys whose values must be replaced with "***"
// before being written to disk. Applied recursively to slog.Group values.
var redactKey = regexp.MustCompile(`(?i)^(token|secret|password|api_key)$`)

// Logger wraps a lumberjack rotating writer with a slog JSON handler.
// It owns its own file handle and exposes Tail/Follow helpers used by
// `surfbot daemon logs`.
type Logger struct {
	path   string
	lj     *lumberjack.Logger
	slog   *slog.Logger
	closer io.Closer
}

// LoggerOptions configures rotation. Zero values fall back to spec defaults
// (10 MB / 5 files / 14 days, compressed).
type LoggerOptions struct {
	MaxSizeMB  int
	MaxBackups int
	MaxAgeDays int
	Compress   bool
}

// NewLogger constructs a Logger that writes to path with rotation.
func NewLogger(path string, opts LoggerOptions) *Logger {
	if opts.MaxSizeMB == 0 {
		opts.MaxSizeMB = 10
	}
	if opts.MaxBackups == 0 {
		opts.MaxBackups = 5
	}
	if opts.MaxAgeDays == 0 {
		opts.MaxAgeDays = 14
	}
	lj := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    opts.MaxSizeMB,
		MaxBackups: opts.MaxBackups,
		MaxAge:     opts.MaxAgeDays,
		Compress:   opts.Compress,
	}
	handler := slog.NewJSONHandler(lj, &slog.HandlerOptions{
		Level:       slog.LevelInfo,
		ReplaceAttr: redactAttr,
	})
	return &Logger{
		path:   path,
		lj:     lj,
		slog:   slog.New(handler),
		closer: lj,
	}
}

// redactAttr replaces sensitive values before they hit disk. Group values
// are walked recursively so nested config blobs cannot leak tokens.
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if redactKey.MatchString(a.Key) {
		return slog.String(a.Key, "***")
	}
	return a
}

// Slog returns the underlying *slog.Logger for daemon code to use directly.
func (l *Logger) Slog() *slog.Logger { return l.slog }

// Path returns the file path for the *current* (un-rotated) log file.
func (l *Logger) Path() string { return l.path }

// Close flushes and closes the rotator.
func (l *Logger) Close() error { return l.closer.Close() }

// Tail returns the last n lines of the current log file. If the file does
// not exist yet it returns an empty slice and no error.
func (l *Logger) Tail(n int) ([]string, error) {
	return tailFile(l.path, n)
}

func tailFile(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	// Simple approach: scan all lines, keep a rolling window of n.
	// Daemon logs are bounded by lumberjack rotation (10 MB) so this is fine.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	ring := make([]string, 0, n)
	for scanner.Scan() {
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return ring, nil
}

// Follow tails the log file until ctx is canceled, writing each new line
// to w. Implementation polls the file size every 250ms and reads from the
// last known offset — no external dependencies.
func (l *Logger) Follow(ctx context.Context, w io.Writer) error {
	f, err := os.Open(l.path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	reader := bufio.NewReader(f)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for {
				line, err := reader.ReadString('\n')
				if len(line) > 0 {
					if _, werr := io.WriteString(w, line); werr != nil {
						return werr
					}
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
			}
		}
	}
}

// FilterSince parses slog JSON log lines and returns only those whose "time"
// field is at or after `since`. Lines that fail to parse are passed through
// unchanged so non-JSON breadcrumbs aren't dropped.
func FilterSince(lines []string, since time.Time) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		t, ok := parseSlogTime(line)
		if !ok || !t.Before(since) {
			out = append(out, line)
		}
	}
	return out
}

func parseSlogTime(line string) (time.Time, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return time.Time{}, false
	}
	var rec struct {
		Time time.Time `json:"time"`
	}
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return time.Time{}, false
	}
	return rec.Time, !rec.Time.IsZero()
}

// FormatLines writes lines to w with a trailing newline each.
func FormatLines(w io.Writer, lines []string) error {
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}
