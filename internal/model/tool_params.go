package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
)

// ErrUnknownTool is returned by ValidateToolConfig when the ToolConfig
// contains a tool key that is not registered in RegisteredToolParams.
var ErrUnknownTool = errors.New("unknown tool")

// ErrInvalidToolParams is returned when a tool key's payload does not
// decode into its registered params struct.
var ErrInvalidToolParams = errors.New("invalid tool params")

// NucleiParams mirrors the subset of nuclei CLI knobs the scheduler
// exposes via tool_config. Field names match SPEC-SCHED1 R20.
type NucleiParams struct {
	Templates   []string      `json:"templates,omitempty"`
	Severity    []string      `json:"severity,omitempty"`
	Tags        []string      `json:"tags,omitempty"`
	ExcludeTags []string      `json:"exclude_tags,omitempty"`
	RateLimit   int           `json:"rate_limit,omitempty"`
	Timeout     time.Duration `json:"timeout,omitempty"`
}

// NaabuParams mirrors naabu CLI knobs.
type NaabuParams struct {
	Ports      string `json:"ports,omitempty"`
	Rate       int    `json:"rate,omitempty"`
	Retries    int    `json:"retries,omitempty"`
	ScanType   string `json:"scan_type,omitempty"`
	BannerGrab bool   `json:"banner_grab,omitempty"`
}

// HttpxParams mirrors httpx CLI knobs.
type HttpxParams struct {
	Threads         int           `json:"threads,omitempty"`
	Probes          []string      `json:"probes,omitempty"`
	FollowRedirects bool          `json:"follow_redirects,omitempty"`
	Timeout         time.Duration `json:"timeout,omitempty"`
}

// SubfinderParams mirrors subfinder knobs.
type SubfinderParams struct {
	Sources    []string `json:"sources,omitempty"`
	AllSources bool     `json:"all_sources,omitempty"`
	Recursive  bool     `json:"recursive,omitempty"`
	Resolvers  []string `json:"resolvers,omitempty"`
}

// DnsxParams mirrors dnsx knobs.
type DnsxParams struct {
	RecordTypes []string `json:"record_types,omitempty"`
	Resolvers   []string `json:"resolvers,omitempty"`
	Retries     int      `json:"retries,omitempty"`
}

// RegisteredToolParams is the authoritative set of tool names whose
// params structs are recognized by ValidateToolConfig. Adding a new
// detection tool to the scheduler means adding a *Params struct and
// registering it here.
var RegisteredToolParams = map[string]reflect.Type{
	"nuclei":    reflect.TypeOf(NucleiParams{}),
	"naabu":     reflect.TypeOf(NaabuParams{}),
	"httpx":     reflect.TypeOf(HttpxParams{}),
	"subfinder": reflect.TypeOf(SubfinderParams{}),
	"dnsx":      reflect.TypeOf(DnsxParams{}),
}

// ValidateToolConfig returns ErrUnknownTool (wrapped with the offending
// tool name) when the ToolConfig contains a key that is not in
// RegisteredToolParams, and ErrInvalidToolParams when a payload fails to
// decode into its registered struct. A nil or empty ToolConfig is valid.
func ValidateToolConfig(tc ToolConfig) error {
	for name, raw := range tc {
		typ, ok := RegisteredToolParams[name]
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownTool, name)
		}
		instance := reflect.New(typ).Interface()
		if err := json.Unmarshal(raw, instance); err != nil {
			return fmt.Errorf("%w: %q: %v", ErrInvalidToolParams, name, err)
		}
	}
	return nil
}
