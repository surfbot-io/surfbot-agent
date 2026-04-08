package intervalsched

// SPEC-X3.1 §8 — on-demand trigger via a flag file dropped by the
// embedded UI. The mechanism is intentionally trivial: the UI writes
// trigger.json atomically (tmp + rename), the scheduler claims it via
// rename to trigger.json.processing, runs the requested profile while
// ignoring the maintenance window (explicit user intent), then deletes
// the processing file. Latency is bounded by the 2 s idle poll.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// triggerFile mirrors the JSON shape the UI writes. Mirrored separately
// from the webui struct to keep this package free of webui imports.
type triggerFile struct {
	ID          string    `json:"id"`
	Profile     string    `json:"profile"`
	RequestedAt time.Time `json:"requested_at"`
}

// TriggerPollInterval is how often the scheduler stat()s the trigger
// directory while idle. Exposed for tests; do not change in production.
var TriggerPollInterval = 2 * time.Second

func triggerFlagPath(dir string) string {
	return filepath.Join(dir, "trigger.json")
}

func triggerProcessingPath(dir string) string {
	return filepath.Join(dir, "trigger.json.processing")
}

// LastTriggerRecord is persisted into ScheduleState.LastTrigger so the
// UI can render the most recent trigger outcome.
type LastTriggerRecord struct {
	ID         string    `json:"id"`
	Profile    string    `json:"profile"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
}

// claimTrigger atomically renames trigger.json to trigger.json.processing
// and returns the parsed payload. Returns (nil, nil) when there is no
// pending trigger. Returns an error only on real I/O / parse failures.
func claimTrigger(dir string) (*triggerFile, error) {
	flag := triggerFlagPath(dir)
	processing := triggerProcessingPath(dir)
	if _, err := os.Stat(flag); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	// If a previous claim is still in flight (crash mid-scan), bail out
	// — we do not want two scans piling up. The stale .processing file
	// gets cleared on the next successful run or by hand.
	if _, err := os.Stat(processing); err == nil {
		return nil, nil
	}
	if err := os.Rename(flag, processing); err != nil {
		// A concurrent claim by another goroutine: treat as no-op.
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	data, err := os.ReadFile(processing)
	if err != nil {
		return nil, err
	}
	var tf triggerFile
	if err := json.Unmarshal(data, &tf); err != nil {
		// Bad JSON: drop the file so the queue does not jam, and surface
		// the error so the caller can log it.
		_ = os.Remove(processing)
		return nil, err
	}
	return &tf, nil
}

// finishTrigger removes the .processing marker. Idempotent.
func finishTrigger(dir string) {
	_ = os.Remove(triggerProcessingPath(dir))
}

// runTriggerLoop is the goroutine that polls the trigger directory and
// invokes runScan for any pending request. It exits when ctx is done.
// The actual scan execution piggybacks on the scheduler's normal scan
// path so cursors and slog events stay consistent.
func (s *IntervalScheduler) runTriggerLoop(ctx context.Context) {
	if s.cfg.TriggerDir == "" {
		return
	}
	// Best-effort: make sure the directory exists. Ignore errors — the
	// daemon may be running before the UI ever creates the file.
	_ = os.MkdirAll(s.cfg.TriggerDir, 0o700)

	t := time.NewTicker(TriggerPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.processTrigger(ctx)
		}
	}
}

func (s *IntervalScheduler) processTrigger(ctx context.Context) {
	tf, err := claimTrigger(s.cfg.TriggerDir)
	if err != nil {
		s.logger.Warn("scheduler.trigger_claim_failed", "err", err)
		return
	}
	if tf == nil {
		return
	}
	profile := ProfileFull
	if tf.Profile == string(ProfileQuick) {
		profile = ProfileQuick
	}
	s.logger.Info("scheduler.trigger_start",
		"id", tf.ID,
		"profile", string(profile),
		"requested_at", tf.RequestedAt)

	started := s.clock.Now()
	var runErr error
	if s.scanner != nil {
		runErr = s.scanner.Run(ctx, profile)
	}
	finished := s.clock.Now()

	// Mirror the result into the schedule state so the UI's poll picks
	// it up via last_full / last_quick. Triggers do NOT advance next_*
	// timers — they are out-of-band and should not perturb the regular
	// cadence.
	status := "ok"
	errStr := ""
	if runErr != nil {
		status = "failed"
		errStr = runErr.Error()
	}
	s.mu.Lock()
	switch profile {
	case ProfileFull:
		s.state.LastFullAt = finished
		s.state.LastFullStatus = status
		s.state.LastFullError = errStr
	case ProfileQuick:
		s.state.LastQuickAt = finished
		s.state.LastQuickStatus = status
		s.state.LastQuickError = errStr
	}
	s.state.LastTrigger = &LastTriggerRecord{
		ID:         tf.ID,
		Profile:    string(profile),
		StartedAt:  started,
		FinishedAt: finished,
		Status:     status,
		Error:      errStr,
	}
	snapshot := s.state
	s.mu.Unlock()

	if s.store != nil {
		if err := s.store.Save(snapshot); err != nil {
			s.logger.Warn("schedule state save failed (trigger)", "err", err)
		}
	}

	finishTrigger(s.cfg.TriggerDir)

	if runErr != nil {
		s.logger.Warn("scheduler.trigger_done",
			"id", tf.ID,
			"profile", string(profile),
			"duration", finished.Sub(started),
			"err", runErr)
	} else {
		s.logger.Info("scheduler.trigger_done",
			"id", tf.ID,
			"profile", string(profile),
			"duration", finished.Sub(started))
	}
}
