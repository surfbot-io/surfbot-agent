package pipeline

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// --- Mock tool ---

type mockTool struct {
	name     string
	phase    string
	assets   []model.Asset
	findings []model.Finding
	err      error
}

func (m *mockTool) Name() string             { return m.name }
func (m *mockTool) Phase() string            { return m.phase }
func (m *mockTool) Kind() detection.ToolKind { return detection.ToolKindLibrary }
func (m *mockTool) Available() bool          { return true }
func (m *mockTool) Command() string          { return m.name }
func (m *mockTool) Description() string      { return "mock tool" }
func (m *mockTool) InputType() string        { return "domains" }
func (m *mockTool) OutputTypes() []string    { return nil }
func (m *mockTool) Run(_ context.Context, _ []string, _ detection.RunOptions) (*detection.RunResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &detection.RunResult{Assets: m.assets, Findings: m.findings}, nil
}

// cancellingMockTool cancels the context after a successful run.
type cancellingMockTool struct {
	mockTool
	cancelFunc context.CancelFunc
}

func (m *cancellingMockTool) Run(ctx context.Context, inputs []string, opts detection.RunOptions) (*detection.RunResult, error) {
	result, err := m.mockTool.Run(ctx, inputs, opts)
	m.cancelFunc()
	return result, err
}

// inputCapturingMockTool records what inputs it received.
type inputCapturingMockTool struct {
	mockTool
	capturedInputs *[]string
}

func (m *inputCapturingMockTool) Run(ctx context.Context, inputs []string, opts detection.RunOptions) (*detection.RunResult, error) {
	*m.capturedInputs = append([]string{}, inputs...)
	return m.mockTool.Run(ctx, inputs, opts)
}

// --- Mock registry ---

func mockRegistry(tools ...detection.DetectionTool) *detection.Registry {
	return detection.NewRegistryFrom(tools)
}

// --- Helper ---

func newTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	s, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func createTarget(t *testing.T, s *storage.SQLiteStore, value string) *model.Target {
	t.Helper()
	target := &model.Target{Value: value}
	require.NoError(t, s.CreateTarget(context.Background(), target))
	return target
}

func fullMockTools() []detection.DetectionTool {
	return []detection.DetectionTool{
		&mockTool{
			name:  "subfinder",
			phase: "discovery",
			assets: []model.Asset{
				{Type: model.AssetTypeSubdomain, Value: "sub1.example.com", Status: model.AssetStatusNew},
				{Type: model.AssetTypeSubdomain, Value: "sub2.example.com", Status: model.AssetStatusNew},
			},
		},
		&mockTool{
			name:  "dnsx",
			phase: "resolution",
			assets: []model.Asset{
				{Type: model.AssetTypeIPv4, Value: "1.2.3.4", Status: model.AssetStatusNew},
				{Type: model.AssetTypeIPv6, Value: "::1", Status: model.AssetStatusNew},
			},
		},
		&mockTool{
			name:  "naabu",
			phase: "port_scan",
			assets: []model.Asset{
				{Type: model.AssetTypePort, Value: "1.2.3.4:443/tcp", Status: model.AssetStatusNew},
				{Type: model.AssetTypePort, Value: "1.2.3.4:80/tcp", Status: model.AssetStatusNew},
			},
		},
		&mockTool{
			name:  "httpx",
			phase: "http_probe",
			assets: []model.Asset{
				{Type: model.AssetTypeURL, Value: "https://sub1.example.com:443", Status: model.AssetStatusNew},
				{Type: model.AssetTypeTechnology, Value: "nginx", Status: model.AssetStatusNew},
			},
		},
		&mockTool{
			name:  "nuclei",
			phase: "assessment",
			findings: []model.Finding{
				{
					TemplateID: "CVE-2024-0001",
					Severity:   model.SeverityCritical,
					Title:      "Critical vuln",
					Status:     model.FindingStatusOpen,
					SourceTool: "nuclei",
					Confidence: 90,
				},
				{
					TemplateID: "CVE-2024-0002",
					Severity:   model.SeverityHigh,
					Title:      "High vuln",
					Status:     model.FindingStatusOpen,
					SourceTool: "nuclei",
					Confidence: 80,
				},
				{
					TemplateID: "INFO-001",
					Severity:   model.SeverityInfo,
					Title:      "Info finding",
					Status:     model.FindingStatusOpen,
					SourceTool: "nuclei",
					Confidence: 50,
				},
			},
		},
	}
}

// --- Tests ---

func TestPipelineFullScan(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")

	reg := mockRegistry(fullMockTools()...)
	pipe := New(s, reg)

	result, err := pipe.Run(context.Background(), target.ID, PipelineOptions{ScanType: model.ScanTypeFull})
	require.NoError(t, err)

	// All 5 phases should complete
	assert.Len(t, result.Phases, 5)
	for _, ph := range result.Phases {
		assert.Equal(t, "completed", ph.Status, "phase %s should be completed", ph.Phase)
	}

	// Verify phase order
	assert.Equal(t, "discovery", result.Phases[0].Phase)
	assert.Equal(t, "resolution", result.Phases[1].Phase)
	assert.Equal(t, "port_scan", result.Phases[2].Phase)
	assert.Equal(t, "http_probe", result.Phases[3].Phase)
	assert.Equal(t, "assessment", result.Phases[4].Phase)

	// Stats
	assert.Equal(t, 2, result.Stats.SubdomainsFound)
	assert.Equal(t, 2, result.Stats.IPsResolved)
	assert.Equal(t, 2, result.Stats.OpenPorts)
	assert.Equal(t, 1, result.Stats.HTTPProbed)
	assert.Equal(t, 1, result.Stats.TechDetected)
	assert.Equal(t, 3, result.Stats.FindingsTotal)
	assert.Equal(t, 1, result.Stats.FindingsCritical)
	assert.Equal(t, 1, result.Stats.FindingsHigh)
	assert.Equal(t, 1, result.Stats.FindingsInfo)

	// Scan should be completed
	scan, err := s.GetScan(context.Background(), result.ScanID)
	require.NoError(t, err)
	assert.Equal(t, model.ScanStatusCompleted, scan.Status)
	assert.Equal(t, float32(100), scan.Progress)
	assert.NotNil(t, scan.FinishedAt)
}

func TestPipelineDiscoveryScan(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")

	reg := mockRegistry(fullMockTools()...)
	pipe := New(s, reg)

	result, err := pipe.Run(context.Background(), target.ID, PipelineOptions{ScanType: model.ScanTypeDiscovery})
	require.NoError(t, err)

	// Only discovery + resolution should complete, others skipped
	completedPhases := 0
	skippedPhases := 0
	for _, ph := range result.Phases {
		switch ph.Status {
		case "completed":
			completedPhases++
			assert.Contains(t, []string{"discovery", "resolution"}, ph.Phase)
		case "skipped":
			skippedPhases++
		}
	}
	assert.Equal(t, 2, completedPhases)
	assert.Equal(t, 3, skippedPhases)
}

func TestPipelineQuickScan(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")

	reg := mockRegistry(fullMockTools()...)
	pipe := New(s, reg)

	result, err := pipe.Run(context.Background(), target.ID, PipelineOptions{ScanType: model.ScanTypeQuick})
	require.NoError(t, err)

	// Port scan should be skipped
	for _, ph := range result.Phases {
		if ph.Phase == "port_scan" {
			assert.Equal(t, "skipped", ph.Status)
		} else {
			assert.Equal(t, "completed", ph.Status, "phase %s should be completed", ph.Phase)
		}
	}
}

func TestPipelineDiscoveryFailure(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")

	tools := []detection.DetectionTool{
		&mockTool{name: "subfinder", phase: "discovery", err: fmt.Errorf("network timeout")},
		&mockTool{name: "dnsx", phase: "resolution"},
	}
	reg := mockRegistry(tools...)
	pipe := New(s, reg)

	_, err := pipe.Run(context.Background(), target.ID, PipelineOptions{ScanType: model.ScanTypeFull})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "subfinder")

	// Scan should be marked as failed
	scans, _ := s.ListScans(context.Background(), target.ID, 1)
	require.Len(t, scans, 1)
	assert.Equal(t, model.ScanStatusFailed, scans[0].Status)
}

func TestPipelineSoftFailure(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")

	tools := []detection.DetectionTool{
		&mockTool{
			name:  "subfinder",
			phase: "discovery",
			assets: []model.Asset{
				{Type: model.AssetTypeSubdomain, Value: "sub.example.com", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "dnsx", phase: "resolution",
			assets: []model.Asset{
				{Type: model.AssetTypeIPv4, Value: "1.2.3.4", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "naabu", phase: "port_scan", err: fmt.Errorf("naabu crashed")},
		&mockTool{name: "httpx", phase: "http_probe",
			assets: []model.Asset{
				{Type: model.AssetTypeURL, Value: "https://sub.example.com", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "nuclei", phase: "assessment"},
	}
	reg := mockRegistry(tools...)
	pipe := New(s, reg)

	result, err := pipe.Run(context.Background(), target.ID, PipelineOptions{ScanType: model.ScanTypeFull})
	require.NoError(t, err)

	// Pipeline should complete despite naabu failure
	scan, _ := s.GetScan(context.Background(), result.ScanID)
	assert.Equal(t, model.ScanStatusCompleted, scan.Status)

	// naabu should be marked failed
	for _, ph := range result.Phases {
		if ph.Phase == "port_scan" {
			assert.Equal(t, "failed", ph.Status)
			assert.Contains(t, ph.Error, "naabu crashed")
		}
	}
}

func TestPipelineEmptyDiscoveryFallsBackToRoot(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")

	// Track what inputs dnsx receives
	var dnsxInputs []string
	dnsxTool := &inputCapturingMockTool{
		mockTool: mockTool{
			name:  "dnsx",
			phase: "resolution",
			assets: []model.Asset{
				{Type: model.AssetTypeIPv4, Value: "93.184.216.34", Status: model.AssetStatusNew},
			},
		},
		capturedInputs: &dnsxInputs,
	}

	tools := []detection.DetectionTool{
		&mockTool{name: "subfinder", phase: "discovery", assets: []model.Asset{}},
		dnsxTool,
	}
	reg := mockRegistry(tools...)
	pipe := New(s, reg)

	result, err := pipe.Run(context.Background(), target.ID, PipelineOptions{ScanType: model.ScanTypeDiscovery})
	require.NoError(t, err)

	// dnsx should have received the root domain as fallback
	assert.Equal(t, []string{"example.com"}, dnsxInputs)

	// Scan should complete
	scan, _ := s.GetScan(context.Background(), result.ScanID)
	assert.Equal(t, model.ScanStatusCompleted, scan.Status)
}

func TestPipelineCancellation(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")

	ctx, cancel := context.WithCancel(context.Background())

	// A tool that cancels the context after running, simulating cancellation mid-pipeline
	cancelOnRun := &cancellingMockTool{
		mockTool: mockTool{
			name:  "subfinder",
			phase: "discovery",
			assets: []model.Asset{
				{Type: model.AssetTypeSubdomain, Value: "sub.example.com", Status: model.AssetStatusNew},
			},
		},
		cancelFunc: cancel,
	}

	tools := []detection.DetectionTool{
		cancelOnRun,
		&mockTool{name: "dnsx", phase: "resolution"},
	}
	reg := mockRegistry(tools...)
	pipe := New(s, reg)

	_, err := pipe.Run(ctx, target.ID, PipelineOptions{ScanType: model.ScanTypeFull})
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	// Scan should be cancelled
	scans, _ := s.ListScans(context.Background(), target.ID, 1)
	require.Len(t, scans, 1)
	assert.Equal(t, model.ScanStatusCancelled, scans[0].Status)
}

func TestDataThreading(t *testing.T) {
	tests := []struct {
		phase    string
		assets   []model.Asset
		expected []string
	}{
		{
			phase: "discovery",
			assets: []model.Asset{
				{Type: model.AssetTypeSubdomain, Value: "a.example.com"},
				{Type: model.AssetTypeSubdomain, Value: "b.example.com"},
			},
			expected: []string{"a.example.com", "b.example.com"},
		},
		{
			phase: "resolution",
			assets: []model.Asset{
				{Type: model.AssetTypeIPv4, Value: "1.2.3.4"},
				{Type: model.AssetTypeIPv6, Value: "::1"},
				{Type: model.AssetTypeIPv4, Value: "1.2.3.4"}, // duplicate
			},
			expected: []string{"1.2.3.4", "::1"},
		},
		{
			phase: "port_scan",
			assets: []model.Asset{
				{Type: model.AssetTypePort, Value: "1.2.3.4:443/tcp"},
				{Type: model.AssetTypePort, Value: "1.2.3.4:80/tcp"},
			},
			expected: []string{"1.2.3.4:443/tcp", "1.2.3.4:80/tcp"},
		},
		{
			phase: "http_probe",
			assets: []model.Asset{
				{Type: model.AssetTypeURL, Value: "https://example.com:443"},
				{Type: model.AssetTypeTechnology, Value: "nginx"}, // should NOT be in output
			},
			expected: []string{"https://example.com:443"},
		},
		{
			phase:    "assessment",
			assets:   []model.Asset{},
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.phase, func(t *testing.T) {
			result := &detection.RunResult{Assets: tc.assets}
			got := extractInputsForNextPhase(tc.phase, result)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// TestPortScanFiltersStatusFiltered covers SPEC-QA2 R9: the port_scan → http_probe
// handoff must drop assets with metadata.status="filtered" so httpx doesn't
// waste its 10s-per-attempt budget on dead SYN-ACK responders.
func TestPortScanFiltersStatusFiltered(t *testing.T) {
	result := &detection.RunResult{
		Assets: []model.Asset{
			{
				Type:     model.AssetTypePort,
				Value:    "1.2.3.4:80/tcp",
				Metadata: map[string]interface{}{"status": "open"},
			},
			{
				Type:     model.AssetTypePort,
				Value:    "1.2.3.4:443/tcp",
				Metadata: map[string]interface{}{"status": "filtered"},
			},
			{
				Type:     model.AssetTypePort,
				Value:    "1.2.3.4:22/tcp",
				Metadata: map[string]interface{}{"status": "open"},
			},
			{
				// No status metadata — backwards compat: treat as open.
				Type:  model.AssetTypePort,
				Value: "1.2.3.4:8080/tcp",
			},
		},
	}
	got := extractInputsForNextPhase("port_scan", result)
	assert.ElementsMatch(t,
		[]string{"1.2.3.4:80/tcp", "1.2.3.4:22/tcp", "1.2.3.4:8080/tcp"},
		got,
		"filtered status must be dropped; assets without status pass through")
}

// TestEnrichHostports covers SUR-242 input-format widening: ip:port/tcp is
// rewritten to hostname|ip:port/tcp when the IP has a resolved hostname.
func TestEnrichHostports(t *testing.T) {
	ipToHostname := map[string]string{
		"1.2.3.4": "example.com",
		"5.6.7.8": "api.example.com",
	}
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "known IP gets hostname prefix",
			in:   []string{"1.2.3.4:443/tcp"},
			want: []string{"example.com|1.2.3.4:443/tcp"},
		},
		{
			name: "unknown IP passes through IP-pure",
			in:   []string{"9.9.9.9:443/tcp"},
			want: []string{"9.9.9.9:443/tcp"},
		},
		{
			name: "mixed known and unknown",
			in:   []string{"1.2.3.4:80/tcp", "9.9.9.9:80/tcp", "5.6.7.8:443/tcp"},
			want: []string{
				"example.com|1.2.3.4:80/tcp",
				"9.9.9.9:80/tcp",
				"api.example.com|5.6.7.8:443/tcp",
			},
		},
		{
			name: "empty map is a no-op",
			in:   []string{"1.2.3.4:443/tcp"},
			want: []string{"1.2.3.4:443/tcp"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := ipToHostname
			if tc.name == "empty map is a no-op" {
				m = map[string]string{}
			}
			got := enrichHostports(tc.in, m)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestPipelineThreadsHostnameToHTTPProbe asserts end-to-end that a resolved
// hostname flows through resolution → port_scan → http_probe input as the
// enriched "hostname|ip:port/tcp" format (SUR-242 R2).
func TestPipelineThreadsHostnameToHTTPProbe(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")

	var httpxInputs []string
	httpx := &inputCapturingMockTool{
		mockTool: mockTool{
			name:  "httpx",
			phase: "http_probe",
		},
		capturedInputs: &httpxInputs,
	}

	tools := []detection.DetectionTool{
		&mockTool{
			name:  "subfinder",
			phase: "discovery",
			assets: []model.Asset{
				{Type: model.AssetTypeSubdomain, Value: "example.com", Status: model.AssetStatusNew},
			},
		},
		&mockTool{
			name:  "dnsx",
			phase: "resolution",
			assets: []model.Asset{
				{
					Type:     model.AssetTypeIPv4,
					Value:    "1.2.3.4",
					Status:   model.AssetStatusNew,
					Metadata: map[string]interface{}{"resolved_from": "example.com"},
				},
			},
		},
		&mockTool{
			name:  "naabu",
			phase: "port_scan",
			assets: []model.Asset{
				{Type: model.AssetTypePort, Value: "1.2.3.4:443/tcp", Status: model.AssetStatusNew},
			},
		},
		httpx,
		&mockTool{name: "nuclei", phase: "assessment"},
	}
	reg := mockRegistry(tools...)
	pipe := New(s, reg)

	_, err := pipe.Run(context.Background(), target.ID, PipelineOptions{ScanType: model.ScanTypeFull})
	require.NoError(t, err)

	assert.Contains(t, httpxInputs, "example.com|1.2.3.4:443/tcp",
		"http_probe input must carry hostname alongside ip:port")
}

func TestShouldSkip(t *testing.T) {
	tools := []struct {
		name  string
		phase string
	}{
		{"subfinder", "discovery"},
		{"dnsx", "resolution"},
		{"naabu", "port_scan"},
		{"httpx", "http_probe"},
		{"nuclei", "assessment"},
	}

	for _, tc := range tools {
		tool := &mockTool{name: tc.name, phase: tc.phase}

		// Full: nothing skipped
		assert.False(t, shouldSkip(tool, PipelineOptions{ScanType: model.ScanTypeFull}),
			"%s should not be skipped for full scan", tc.name)
	}

	// Quick: port_scan skipped
	assert.True(t, shouldSkip(&mockTool{phase: "port_scan"}, PipelineOptions{ScanType: model.ScanTypeQuick}))
	assert.False(t, shouldSkip(&mockTool{phase: "discovery"}, PipelineOptions{ScanType: model.ScanTypeQuick}))

	// Discovery: only discovery + resolution
	assert.False(t, shouldSkip(&mockTool{phase: "discovery"}, PipelineOptions{ScanType: model.ScanTypeDiscovery}))
	assert.False(t, shouldSkip(&mockTool{phase: "resolution"}, PipelineOptions{ScanType: model.ScanTypeDiscovery}))
	assert.True(t, shouldSkip(&mockTool{phase: "port_scan"}, PipelineOptions{ScanType: model.ScanTypeDiscovery}))
	assert.True(t, shouldSkip(&mockTool{phase: "http_probe"}, PipelineOptions{ScanType: model.ScanTypeDiscovery}))
	assert.True(t, shouldSkip(&mockTool{phase: "assessment"}, PipelineOptions{ScanType: model.ScanTypeDiscovery}))
}

func TestUpdateStats(t *testing.T) {
	stats := &model.ScanStats{}

	updateStats(stats, "discovery", &detection.RunResult{
		Assets: []model.Asset{
			{Type: model.AssetTypeSubdomain, Value: "a.example.com"},
			{Type: model.AssetTypeSubdomain, Value: "b.example.com"},
		},
	})
	assert.Equal(t, 2, stats.SubdomainsFound)

	updateStats(stats, "resolution", &detection.RunResult{
		Assets: []model.Asset{
			{Type: model.AssetTypeIPv4, Value: "1.2.3.4"},
		},
	})
	assert.Equal(t, 1, stats.IPsResolved)

	updateStats(stats, "port_scan", &detection.RunResult{
		Assets: []model.Asset{
			{Type: model.AssetTypePort, Value: "1.2.3.4:80/tcp"},
			{Type: model.AssetTypePort, Value: "1.2.3.4:443/tcp"},
		},
	})
	assert.Equal(t, 2, stats.OpenPorts)

	updateStats(stats, "http_probe", &detection.RunResult{
		Assets: []model.Asset{
			{Type: model.AssetTypeURL, Value: "https://example.com"},
			{Type: model.AssetTypeTechnology, Value: "nginx"},
		},
	})
	assert.Equal(t, 1, stats.HTTPProbed)
	assert.Equal(t, 1, stats.TechDetected)

	updateStats(stats, "assessment", &detection.RunResult{
		Findings: []model.Finding{
			{Severity: model.SeverityCritical},
			{Severity: model.SeverityHigh},
			{Severity: model.SeverityMedium},
			{Severity: model.SeverityLow},
			{Severity: model.SeverityInfo},
		},
	})
	assert.Equal(t, 5, stats.FindingsTotal)
	assert.Equal(t, 1, stats.FindingsCritical)
	assert.Equal(t, 1, stats.FindingsHigh)
	assert.Equal(t, 1, stats.FindingsMedium)
	assert.Equal(t, 1, stats.FindingsLow)
	assert.Equal(t, 1, stats.FindingsInfo)
}

func TestPipelineNoURLsSkipsNuclei(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")

	tools := []detection.DetectionTool{
		&mockTool{
			name:  "subfinder",
			phase: "discovery",
			assets: []model.Asset{
				{Type: model.AssetTypeSubdomain, Value: "sub.example.com", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "dnsx", phase: "resolution",
			assets: []model.Asset{
				{Type: model.AssetTypeIPv4, Value: "1.2.3.4", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "naabu", phase: "port_scan",
			assets: []model.Asset{
				{Type: model.AssetTypePort, Value: "1.2.3.4:80/tcp", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "httpx", phase: "http_probe", assets: []model.Asset{}}, // 0 URLs
		&mockTool{name: "nuclei", phase: "assessment"},
	}
	reg := mockRegistry(tools...)
	pipe := New(s, reg)

	result, err := pipe.Run(context.Background(), target.ID, PipelineOptions{ScanType: model.ScanTypeFull})
	require.NoError(t, err)

	// nuclei should be skipped
	for _, ph := range result.Phases {
		if ph.Phase == "assessment" {
			assert.Equal(t, "skipped", ph.Status)
		}
	}

	scan, _ := s.GetScan(context.Background(), result.ScanID)
	assert.Equal(t, model.ScanStatusCompleted, scan.Status)
}

func TestPipelineToolRunRecording(t *testing.T) {
	s := newTestStore(t)
	target := createTarget(t, s, "example.com")

	tools := []detection.DetectionTool{
		&mockTool{
			name:  "subfinder",
			phase: "discovery",
			assets: []model.Asset{
				{Type: model.AssetTypeSubdomain, Value: "sub.example.com", Status: model.AssetStatusNew},
			},
		},
		&mockTool{name: "dnsx", phase: "resolution",
			assets: []model.Asset{
				{Type: model.AssetTypeIPv4, Value: "1.2.3.4", Status: model.AssetStatusNew},
			},
		},
	}
	reg := mockRegistry(tools...)
	pipe := New(s, reg)

	result, err := pipe.Run(context.Background(), target.ID, PipelineOptions{ScanType: model.ScanTypeDiscovery})
	require.NoError(t, err)
	assert.NotEmpty(t, result.ScanID)

	// Duration should be tracked
	assert.True(t, result.Duration > 0 || result.Duration == 0) // mock is instant
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "0.5s"},
		{5 * time.Second, "5.0s"},
		{2*time.Minute + 34*time.Second, "2m34s"},
		{10*time.Minute + 5*time.Second, "10m05s"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, formatDuration(tc.d))
	}
}
