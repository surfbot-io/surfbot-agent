package intervalsched

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// ScheduleState is the persisted cursor file written next to
// daemon.state.json. Loaded once at scheduler start; missing or corrupt
// → start fresh from now. Atomic writes via tmp + rename, same pattern
// as the X1 daemon StateStore.
type ScheduleState struct {
	LastFullAt      time.Time `json:"last_full_at,omitempty"`
	LastFullStatus  string    `json:"last_full_status,omitempty"`
	LastFullError   string    `json:"last_full_error,omitempty"`
	LastQuickAt     time.Time `json:"last_quick_at,omitempty"`
	LastQuickStatus string    `json:"last_quick_status,omitempty"`
	LastQuickError  string    `json:"last_quick_error,omitempty"`
	NextFullAt      time.Time `json:"next_full_at,omitempty"`
	NextQuickAt     time.Time `json:"next_quick_at,omitempty"`
}

// ScheduleStateStore reads/writes ScheduleState atomically.
type ScheduleStateStore struct {
	path string
	mu   sync.Mutex
}

func NewScheduleStateStore(path string) *ScheduleStateStore {
	return &ScheduleStateStore{path: path}
}

func (s *ScheduleStateStore) Path() string { return s.path }

func (s *ScheduleStateStore) Load() (ScheduleState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st ScheduleState
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return st, nil
		}
		return st, fmt.Errorf("reading schedule state: %w", err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("parsing schedule state: %w", err)
	}
	return st, nil
}

func (s *ScheduleStateStore) Save(st ScheduleState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling schedule state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing temp schedule state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming schedule state: %w", err)
	}
	return nil
}
