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

func TestRegisteredToolParams_Covers5Tools(t *testing.T) {
	want := []string{"nuclei", "naabu", "httpx", "subfinder", "dnsx"}
	for _, name := range want {
		typ, ok := RegisteredToolParams[name]
		require.True(t, ok, "missing registration for %q", name)
		// Confirm the type is a struct (reflect.Type of the Params value).
		assert.Equal(t, reflect.Struct, typ.Kind())
	}
}
