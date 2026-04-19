package model

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateToolConfig_UnknownTool(t *testing.T) {
	tc := ToolConfig{
		"amass": json.RawMessage(`{"brute": true}`),
	}
	err := ValidateToolConfig(tc)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnknownTool), "expected ErrUnknownTool, got %v", err)
	assert.Contains(t, err.Error(), "amass")
}

func TestValidateToolConfig_InvalidPayload(t *testing.T) {
	tc := ToolConfig{
		"nuclei": json.RawMessage(`"not an object"`),
	}
	err := ValidateToolConfig(tc)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidToolParams), "expected ErrInvalidToolParams, got %v", err)
}

func TestValidateToolConfig_AllKnownTools(t *testing.T) {
	tc := ToolConfig{}
	require.NoError(t, SetTool(tc, "nuclei", NucleiParams{Severity: []string{"critical"}}))
	require.NoError(t, SetTool(tc, "naabu", NaabuParams{Ports: "top-1000"}))
	require.NoError(t, SetTool(tc, "httpx", HttpxParams{Threads: 50}))
	require.NoError(t, SetTool(tc, "subfinder", SubfinderParams{AllSources: true}))
	require.NoError(t, SetTool(tc, "dnsx", DnsxParams{RecordTypes: []string{"A", "AAAA"}}))
	assert.NoError(t, ValidateToolConfig(tc))
}

func TestValidateToolConfig_Empty(t *testing.T) {
	assert.NoError(t, ValidateToolConfig(nil))
	assert.NoError(t, ValidateToolConfig(ToolConfig{}))
}

func TestNucleiParams_JSONRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		v    NucleiParams
	}{
		{"zero", NucleiParams{}},
		{"populated", NucleiParams{
			Templates:   []string{"cves/", "exposed-panels/"},
			Severity:    []string{"critical", "high"},
			Tags:        []string{"cve"},
			ExcludeTags: []string{"intrusive"},
			RateLimit:   150,
			Timeout:     30 * time.Second,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := json.Marshal(c.v)
			require.NoError(t, err)
			var got NucleiParams
			require.NoError(t, json.Unmarshal(raw, &got))
			assert.Equal(t, c.v, got)
		})
	}
}

func TestNucleiParams_OmitEmpty(t *testing.T) {
	raw, err := json.Marshal(NucleiParams{})
	require.NoError(t, err)
	assert.Equal(t, "{}", string(raw), "zero-valued params must marshal to {} with omitempty tags")
}

func TestNaabuParams_JSONRoundTrip(t *testing.T) {
	orig := NaabuParams{Ports: "top-1000", Rate: 1000, Retries: 3, ScanType: "syn", BannerGrab: true}
	raw, err := json.Marshal(orig)
	require.NoError(t, err)
	var got NaabuParams
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, orig, got)
}

func TestHttpxParams_JSONRoundTrip(t *testing.T) {
	orig := HttpxParams{
		Threads:         25,
		Probes:          []string{"status", "title", "tech"},
		FollowRedirects: true,
		Timeout:         5 * time.Second,
	}
	raw, err := json.Marshal(orig)
	require.NoError(t, err)
	var got HttpxParams
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, orig, got)
}

func TestSubfinderParams_JSONRoundTrip(t *testing.T) {
	orig := SubfinderParams{
		Sources:    []string{"crtsh", "virustotal"},
		AllSources: false,
		Recursive:  true,
		Resolvers:  []string{"1.1.1.1"},
	}
	raw, err := json.Marshal(orig)
	require.NoError(t, err)
	var got SubfinderParams
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, orig, got)
}

func TestDnsxParams_JSONRoundTrip(t *testing.T) {
	orig := DnsxParams{RecordTypes: []string{"A", "AAAA", "CNAME"}, Resolvers: []string{"1.1.1.1"}, Retries: 2}
	raw, err := json.Marshal(orig)
	require.NoError(t, err)
	var got DnsxParams
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, orig, got)
}

func TestDefaultParams_RoundTripJSON(t *testing.T) {
	cases := []struct {
		name string
		val  any
	}{
		{"nuclei", DefaultNucleiParams()},
		{"naabu", DefaultNaabuParams()},
		{"httpx", DefaultHttpxParams()},
		{"subfinder", DefaultSubfinderParams()},
		{"dnsx", DefaultDnsxParams()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := json.Marshal(c.val)
			require.NoError(t, err)
			ptr := reflect.New(reflect.TypeOf(c.val)).Interface()
			require.NoError(t, json.Unmarshal(raw, ptr))
			got := reflect.ValueOf(ptr).Elem().Interface()
			assert.Equal(t, c.val, got)
		})
	}
}

// Each Default constructor's values are checked against the call-site
// constants they replaced. Failure here means the defaults have drifted
// from the pre-SCHED1 hardcoded behavior — fix the default, not the test.
func TestDefaultNucleiParams_MatchesPreSCHED1Behavior(t *testing.T) {
	got := DefaultNucleiParams()
	assert.Equal(t, []string{"critical", "high", "medium", "low", "info"}, got.Severity,
		"pre-1.2c nuclei.go used severity=critical,high,medium,low,info")
	assert.Equal(t, 150, got.RateLimit,
		"pre-1.2c nuclei.go hardcoded rateLimit=150")
	assert.Equal(t, 5*time.Second, got.Timeout,
		"pre-1.2c nuclei.go hardcoded NetworkConfig.Timeout=5s")
}

func TestDefaultNaabuParams_MatchesPreSCHED1Behavior(t *testing.T) {
	got := DefaultNaabuParams()
	assert.Equal(t, "top100", got.Ports,
		"pre-1.2c naabu.go default port set is Top100Ports")
	assert.Equal(t, 20, got.Rate,
		"pre-1.2c naabu.go used defaultConcurrency=20 when RateLimit unset")
	assert.True(t, got.BannerGrab,
		"pre-1.2c naabu.go banner grab defaulted on; --no-banner=false")
}

func TestDefaultHttpxParams_MatchesPreSCHED1Behavior(t *testing.T) {
	got := DefaultHttpxParams()
	assert.Equal(t, 50, got.Threads,
		"pre-1.2c httpx.go used concurrency=50 when RateLimit unset")
	assert.Equal(t, 10*time.Second, got.Timeout,
		"pre-1.2c httpx.go used http.Client.Timeout=10s")
	assert.True(t, got.FollowRedirects,
		"pre-1.2c httpx.go followed up to 3 redirects")
}

func TestDefaultSubfinderParams_MatchesPreSCHED1Behavior(t *testing.T) {
	got := DefaultSubfinderParams()
	assert.True(t, got.AllSources,
		"pre-1.2c subfinder.go set Options.All=true (every passive source)")
}

func TestDefaultDnsxParams_MatchesPreSCHED1Behavior(t *testing.T) {
	got := DefaultDnsxParams()
	assert.Contains(t, got.RecordTypes, "A")
	assert.Contains(t, got.RecordTypes, "AAAA")
	assert.Contains(t, got.RecordTypes, "CNAME",
		"pre-1.2c dnsx.go resolved A+AAAA via LookupHost and CNAME via LookupCNAME")
}

func TestRegisteredToolParams_Covers5Tools(t *testing.T) {
	want := []string{"nuclei", "naabu", "httpx", "subfinder", "dnsx"}
	for _, name := range want {
		typ, ok := RegisteredToolParams[name]
		require.True(t, ok, "missing registration for %q", name)
		// Confirm the type is a struct (reflect.Type of the Params value).
		assert.Equal(t, reflect.Struct, typ.Kind())
	}
}
