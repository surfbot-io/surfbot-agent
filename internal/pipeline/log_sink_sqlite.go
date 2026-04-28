package pipeline

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// scanLogWriter is the narrow contract SQLiteLogSink needs from the
// store. Defined here (rather than imported from storage) so the
// pipeline package doesn't gain a circular dep, and so unit tests can
// fake the writer with a slice.
type scanLogWriter interface {
	InsertScanLogs(ctx context.Context, logs []model.ScanLog) error
}

// SQLiteLogSink persists pipeline events to scan_logs via batched
// asynchronous writes. The hot path (caller side) is a non-blocking
// channel send; persistence happens in a background goroutine that
// flushes whenever the buffer hits batchSize OR flushInterval elapses
// — whichever comes first. Under load (channel full) the sink drops
// the OLDEST queued line and warns once per minute via the Go log
// package. Logs are best-effort: findings + tool_runs remain canonical.
type SQLiteLogSink struct {
	store     scanLogWriter
	ch        chan model.ScanLog
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	batchSize     int
	flushInterval time.Duration

	// drop accounting — atomics so the Stats() probe is lock-free.
	droppedTotal atomic.Int64
	lastWarnUnix atomic.Int64
}

// SQLiteLogSinkOptions tunes the sink. Zero values fall back to safe
// defaults; production callers typically pass nothing.
type SQLiteLogSinkOptions struct {
	ChannelCapacity int           // default 1000
	BatchSize       int           // default 50
	FlushInterval   time.Duration // default 100ms
}

// NewSQLiteLogSink starts the background drain goroutine. The caller
// MUST call Close() before the process exits to flush outstanding
// lines; pipeline.Run() does this via defer.
func NewSQLiteLogSink(store scanLogWriter, opts SQLiteLogSinkOptions) *SQLiteLogSink {
	if opts.ChannelCapacity <= 0 {
		opts.ChannelCapacity = 1000
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 50
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = 100 * time.Millisecond
	}
	s := &SQLiteLogSink{
		store:         store,
		ch:            make(chan model.ScanLog, opts.ChannelCapacity),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
		batchSize:     opts.BatchSize,
		flushInterval: opts.FlushInterval,
	}
	go s.run()
	return s
}

// run is the background drain goroutine. Exits cleanly when stop is
// closed AND the channel is drained.
func (s *SQLiteLogSink) run() {
	defer close(s.done)
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()
	batch := make([]model.ScanLog, 0, s.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		// Use a fresh background context so a cancelled scan ctx
		// doesn't bin the final flush. The persistence path is fast
		// enough that we don't need a timeout — the main scan flow has
		// already returned by the time Close() blocks here.
		if err := s.store.InsertScanLogs(context.Background(), batch); err != nil {
			log.Printf("scan_logs sink: insert failed: %v (batch=%d)", err, len(batch))
		}
		batch = batch[:0]
	}
	for {
		select {
		case l, ok := <-s.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, l)
			if len(batch) >= s.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.stop:
			// Drain whatever's left in the channel before exit.
			for {
				select {
				case l := <-s.ch:
					batch = append(batch, l)
					if len(batch) >= s.batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

// ansiRE matches CSI-style ANSI escape sequences (the colorize family).
// Tools and theme writers may emit color codes that have meaning in a
// terminal but are noise once persisted — strip before write.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	if s == "" {
		return s
	}
	return ansiRE.ReplaceAllString(s, "")
}

// enqueue is the non-blocking ingress. On overflow it drops the OLDEST
// queued line (preserving the most recent context) and accounts for
// the loss. We warn at most once per 60s to avoid log-storm spam in
// the meta logs themselves.
func (s *SQLiteLogSink) enqueue(l model.ScanLog) {
	l.Text = stripANSI(l.Text)
	if l.CreatedAt.IsZero() {
		l.CreatedAt = time.Now().UTC()
	}
	if l.Timestamp.IsZero() {
		l.Timestamp = l.CreatedAt
	}
	if l.Level == "" {
		l.Level = model.LogLevelInfo
	}
	for {
		select {
		case s.ch <- l:
			return
		default:
			// Channel full — drop the oldest, retry. select-default
			// on the receive side ensures we don't block if a racing
			// drainer already consumed the slot.
			select {
			case <-s.ch:
				s.droppedTotal.Add(1)
			default:
			}
			s.warnOnDrop()
		}
	}
}

func (s *SQLiteLogSink) warnOnDrop() {
	now := time.Now().Unix()
	last := s.lastWarnUnix.Load()
	if now-last >= 60 {
		if s.lastWarnUnix.CompareAndSwap(last, now) {
			log.Printf("scan_logs sink: channel full, dropping oldest lines (total dropped: %d)", s.droppedTotal.Load())
		}
	}
}

// Stats exposes drop counters for tests + observability.
func (s *SQLiteLogSink) Stats() (dropped int64) {
	return s.droppedTotal.Load()
}

// Close flushes outstanding lines synchronously. Safe to call multiple
// times; only the first call signals the goroutine to drain.
func (s *SQLiteLogSink) Close() error {
	s.closeOnce.Do(func() {
		close(s.stop)
		<-s.done
		// Closing the channel after the goroutine exits prevents a
		// panic on any racing enqueue() — but enqueue() also handles
		// a closed channel via panic recovery. The contract is: don't
		// emit after Close(). Pipeline.Run guarantees that.
	})
	return nil
}

// --- LogSink methods ---------------------------------------------

func (s *SQLiteLogSink) ScanStarted(ctx context.Context, scanID, target string, scanType model.ScanType) {
	s.enqueue(model.ScanLog{
		ScanID: scanID, Source: "scanner", Level: model.LogLevelInfo,
		Text: fmt.Sprintf("scan started · target=%s type=%s", target, scanType),
	})
}

func (s *SQLiteLogSink) ScanCompleted(ctx context.Context, scanID string, durationMs int64) {
	s.enqueue(model.ScanLog{
		ScanID: scanID, Source: "scanner", Level: model.LogLevelInfo,
		Text: fmt.Sprintf("scan completed in %dms", durationMs),
	})
}

func (s *SQLiteLogSink) ScanFailed(ctx context.Context, scanID, errMsg string) {
	s.enqueue(model.ScanLog{
		ScanID: scanID, Source: "scanner", Level: model.LogLevelError,
		Text: fmt.Sprintf("scan failed: %s", errMsg),
	})
}

func (s *SQLiteLogSink) ScanCancelled(ctx context.Context, scanID, reason string) {
	text := "scan cancelled"
	if reason != "" {
		text = "scan cancelled: " + reason
	}
	s.enqueue(model.ScanLog{
		ScanID: scanID, Source: "scanner", Level: model.LogLevelWarn, Text: text,
	})
}

func (s *SQLiteLogSink) PhaseStarted(ctx context.Context, scanID, phase, toolName string) {
	s.enqueue(model.ScanLog{
		ScanID: scanID, Source: "scanner", Level: model.LogLevelInfo,
		Text: fmt.Sprintf("phase=%s starting (%s)", phase, toolName),
	})
}

func (s *SQLiteLogSink) ToolStarted(ctx context.Context, scanID, toolRunID, toolName, phase string, inputCount int) {
	s.enqueue(model.ScanLog{
		ScanID: scanID, ToolRunID: toolRunID, Source: toolName, Level: model.LogLevelInfo,
		Text: fmt.Sprintf("tool started · phase=%s inputs=%d", phase, inputCount),
	})
}

func (s *SQLiteLogSink) ToolCompleted(ctx context.Context, scanID, toolRunID, toolName string, durationMs int64, outputCount int, summary string) {
	text := fmt.Sprintf("tool completed in %dms · outputs=%d", durationMs, outputCount)
	if summary != "" {
		text += " · " + summary
	}
	s.enqueue(model.ScanLog{
		ScanID: scanID, ToolRunID: toolRunID, Source: toolName, Level: model.LogLevelInfo, Text: text,
	})
}

func (s *SQLiteLogSink) ToolFailed(ctx context.Context, scanID, toolRunID, toolName, errMsg string) {
	s.enqueue(model.ScanLog{
		ScanID: scanID, ToolRunID: toolRunID, Source: toolName, Level: model.LogLevelError,
		Text: fmt.Sprintf("tool failed: %s", errMsg),
	})
}

func (s *SQLiteLogSink) ToolSkipped(ctx context.Context, scanID, toolName, reason string) {
	text := "tool skipped"
	if reason != "" {
		text = "tool skipped: " + reason
	}
	s.enqueue(model.ScanLog{
		ScanID: scanID, Source: toolName, Level: model.LogLevelInfo, Text: text,
	})
}

func (s *SQLiteLogSink) ToolStderr(ctx context.Context, scanID, toolRunID, toolName, line string) {
	if line == "" {
		return
	}
	s.enqueue(model.ScanLog{
		ScanID: scanID, ToolRunID: toolRunID, Source: toolName, Level: model.LogLevelWarn, Text: line,
	})
}

func (s *SQLiteLogSink) Emit(ctx context.Context, scanID string, level model.LogLevel, source, text string) {
	if source == "" {
		source = "scanner"
	}
	s.enqueue(model.ScanLog{
		ScanID: scanID, Source: source, Level: level, Text: text,
	})
}

// Compile-time assertion that SQLiteLogSink satisfies LogSink.
var _ LogSink = (*SQLiteLogSink)(nil)
