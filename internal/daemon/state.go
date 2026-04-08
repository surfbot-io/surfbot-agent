package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// State is the JSON shape persisted to daemon.state.json. It is the source
// of truth `surfbot daemon status` reads from.
type State struct {
	Version        string    `json:"version"`
	PID            int       `json:"pid"`
	StartedAt      time.Time `json:"started_at"`
	LastScanAt     time.Time `json:"last_scan_at,omitempty"`
	LastScanStatus string    `json:"last_scan_status,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	NextScanAt     time.Time `json:"next_scan_at,omitempty"`
	// WrittenAt is refreshed on every heartbeat write. The embedded UI
	// (SPEC-X3.1) infers daemon liveness by comparing this to the
	// configured heartbeat interval — see internal/webui daemon status
	// handler. Older state files predating SPEC-X3.1 may omit it; readers
	// must treat the zero value as "unknown" and fall back to running.
	WrittenAt time.Time `json:"written_at,omitempty"`
}

// StateStore reads and writes the daemon state file atomically.
// All writes go through a temp file + os.Rename so a crash mid-write
// cannot leave a partially-written JSON document on disk.
type StateStore struct {
	path string
	mu   sync.Mutex
}

func NewStateStore(path string) *StateStore { return &StateStore{path: path} }

// Path returns the file path the store reads/writes.
func (s *StateStore) Path() string { return s.path }

// Load returns the current state or a zero State if the file does not
// exist yet. Corrupt files return an error.
func (s *StateStore) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st State
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return st, nil
		}
		return st, fmt.Errorf("reading state file: %w", err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("parsing state file: %w", err)
	}
	return st, nil
}

// Save writes the state atomically: write to a sibling .tmp file then
// rename over the target. On POSIX rename is atomic; on Windows
// os.Rename uses MoveFileEx which is also atomic.
func (s *StateStore) Save(st State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing temp state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming state file: %w", err)
	}
	return nil
}

// Update loads the current state, applies fn, and saves the result.
func (s *StateStore) Update(fn func(*State)) error {
	st, err := s.Load()
	if err != nil {
		return err
	}
	fn(&st)
	return s.Save(st)
}
