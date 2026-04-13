package intervalsched

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

// ScheduleConfig is the JSON-friendly representation of the scheduler
// configuration persisted to schedule.config.json. It uses string
// durations ("24h") for human readability and survives daemon restarts.
type ScheduleConfig struct {
	Enabled            bool                `json:"enabled"`
	FullScanInterval   string              `json:"full_scan_interval"`
	QuickCheckInterval string              `json:"quick_check_interval"`
	Jitter             string              `json:"jitter"`
	RunOnStart         bool                `json:"run_on_start"`
	QuickCheckTools    []string            `json:"quick_check_tools"`
	MaintenanceWindow  ScheduleConfigWindow `json:"maintenance_window"`
}

// ScheduleConfigWindow is the JSON-friendly maintenance window config.
type ScheduleConfigWindow struct {
	Enabled  bool   `json:"enabled"`
	Start    string `json:"start"`    // "HH:MM"
	End      string `json:"end"`      // "HH:MM"
	Timezone string `json:"timezone"` // IANA timezone
}

// ScheduleConfigStore reads/writes ScheduleConfig atomically using the
// same tmp + rename pattern as ScheduleStateStore.
type ScheduleConfigStore struct {
	path string
	mu   sync.Mutex
}

// NewScheduleConfigStore creates a store for schedule.config.json at the
// given path.
func NewScheduleConfigStore(path string) *ScheduleConfigStore {
	return &ScheduleConfigStore{path: path}
}

// Path returns the config file path.
func (s *ScheduleConfigStore) Path() string { return s.path }

// Exists reports whether the config file exists.
func (s *ScheduleConfigStore) Exists() bool {
	_, err := os.Stat(s.path)
	return err == nil
}

// Load reads and parses the schedule config file. Returns
// os.ErrNotExist (unwrapped) when the file does not exist so callers
// can fall back to config.yaml defaults.
func (s *ScheduleConfigStore) Load() (ScheduleConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sc ScheduleConfig
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sc, os.ErrNotExist
		}
		return sc, fmt.Errorf("reading schedule config: %w", err)
	}
	if err := json.Unmarshal(data, &sc); err != nil {
		return sc, fmt.Errorf("parsing schedule config: %w", err)
	}
	return sc, nil
}

// Save persists the schedule config atomically.
func (s *ScheduleConfigStore) Save(sc ScheduleConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling schedule config: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing temp schedule config: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming schedule config: %w", err)
	}
	return nil
}
