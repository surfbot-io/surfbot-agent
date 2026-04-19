package detection

import (
	"context"
	"errors"
	"testing"
	"time"

	nuclei "github.com/projectdiscovery/nuclei/v3/lib"
	nucleimodel "github.com/projectdiscovery/nuclei/v3/pkg/model"
	"github.com/projectdiscovery/nuclei/v3/pkg/model/types/severity"
	"github.com/projectdiscovery/nuclei/v3/pkg/model/types/stringslice"
	"github.com/projectdiscovery/nuclei/v3/pkg/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// fakeNucleiEngine satisfies the nucleiEngine interface and lets tests
// control timing + capture inputs.
type fakeNucleiEngine struct {
	loaded      []string
	executeWait time.Duration // when > 0, ExecuteWithCallback respects ctxFn
	ctxFn       func() context.Context
	executeErr  error
}

func (f *fakeNucleiEngine) LoadTargets(targets []string, _ bool) {
	f.loaded = append(f.loaded, targets...)
}

func (f *fakeNucleiEngine) ExecuteWithCallback(_ ...func(*output.ResultEvent)) error {
	if f.executeWait > 0 && f.ctxFn != nil {
		select {
		case <-time.After(f.executeWait):
		case <-f.ctxFn().Done():
			return f.ctxFn().Err()
		}
	}
	return f.executeErr
}

func (f *fakeNucleiEngine) Close() {}

func swapNucleiEngineFactory(t *testing.T, fn nucleiEngineFactory) {
	t.Helper()
	prev := nucleiEngineFactoryOverride
	nucleiEngineFactoryOverride = fn
	t.Cleanup(func() { nucleiEngineFactoryOverride = prev })
}

func TestResolveNucleiParams_TypedOverridesWin(t *testing.T) {
	got := resolveNucleiParams(RunOptions{
		NucleiParams: &model.NucleiParams{
			Severity:  []string{"critical"},
			RateLimit: 50,
			Timeout:   3 * time.Second,
		},
	})
	assert.Equal(t, []string{"critical"}, got.Severity)
	assert.Equal(t, 50, got.RateLimit)
	assert.Equal(t, 3*time.Second, got.Timeout)
}

func TestResolveNucleiParams_FallsBackToDefaults(t *testing.T) {
	got := resolveNucleiParams(RunOptions{})
	def := model.DefaultNucleiParams()
	assert.Equal(t, def.Severity, got.Severity)
	assert.Equal(t, def.RateLimit, got.RateLimit)
	assert.Equal(t, def.Timeout, got.Timeout)
}

func TestResolveNucleiParams_PartialTypedFillsFromDefaults(t *testing.T) {
	got := resolveNucleiParams(RunOptions{
		NucleiParams: &model.NucleiParams{Severity: []string{"high"}},
	})
	def := model.DefaultNucleiParams()
	assert.Equal(t, []string{"high"}, got.Severity)
	assert.Equal(t, def.RateLimit, got.RateLimit, "unset RateLimit must inherit default")
	assert.Equal(t, def.Timeout, got.Timeout, "unset Timeout must inherit default")
}

func TestNuclei_ParamsPropagation_Severity(t *testing.T) {
	// Skip the heavy template-install path by using a fake engine, but the
	// resolved-params side channel still records what would be passed.
	swapNucleiEngineFactory(t, func(_ context.Context, _ ...nuclei.NucleiSDKOptions) (nucleiEngine, error) {
		return &fakeNucleiEngine{}, nil
	})
	defer func() { nucleiLastResolvedParams = model.NucleiParams{} }()

	n := NewNucleiTool()
	_, err := n.Run(context.Background(), []string{"https://example.com"}, RunOptions{
		NucleiParams: &model.NucleiParams{Severity: []string{"critical", "high"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"critical", "high"}, nucleiLastResolvedParams.Severity)
}

func TestNuclei_ParamsPropagation_RateLimit(t *testing.T) {
	swapNucleiEngineFactory(t, func(_ context.Context, _ ...nuclei.NucleiSDKOptions) (nucleiEngine, error) {
		return &fakeNucleiEngine{}, nil
	})
	defer func() { nucleiLastResolvedParams = model.NucleiParams{} }()

	n := NewNucleiTool()
	_, err := n.Run(context.Background(), []string{"https://example.com"}, RunOptions{
		NucleiParams: &model.NucleiParams{RateLimit: 25},
	})
	require.NoError(t, err)
	assert.Equal(t, 25, nucleiLastResolvedParams.RateLimit)
}

func TestNuclei_CtxCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := &fakeNucleiEngine{
		executeWait: 5 * time.Second,
		ctxFn:       func() context.Context { return ctx },
	}
	swapNucleiEngineFactory(t, func(_ context.Context, _ ...nuclei.NucleiSDKOptions) (nucleiEngine, error) {
		return fake, nil
	})

	n := NewNucleiTool()
	done := make(chan error, 1)
	go func() {
		_, err := n.Run(ctx, []string{"https://example.com"}, RunOptions{})
		done <- err
	}()
	time.AfterFunc(50*time.Millisecond, cancel)

	select {
	case err := <-done:
		require.Error(t, err)
		assert.True(t, errors.Is(err, context.Canceled), "want ctx.Canceled, got %v", err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return within 500ms after ctx cancel")
	}
}

func TestNucleiResultMapping(t *testing.T) {
	event := &output.ResultEvent{
		TemplateID: "ssl-expired",
		Matched:    "https://example.com:443",
		Info: nucleimodel.Info{
			Name:        "Expired SSL Certificate",
			Description: "The SSL certificate has expired",
			Remediation: "Renew the certificate",
			SeverityHolder: severity.Holder{
				Severity: severity.Medium,
			},
			Reference: stringslice.NewRawStringSlice([]string{"https://cwe.mitre.org/data/definitions/295.html"}),
		},
	}

	finding := mapNucleiEvent(event)

	assert.Equal(t, "ssl-expired", finding.TemplateID)
	assert.Equal(t, "Expired SSL Certificate", finding.Title)
	assert.Equal(t, model.SeverityMedium, finding.Severity)
	assert.Equal(t, "The SSL certificate has expired", finding.Description)
	assert.Equal(t, "Renew the certificate", finding.Remediation)
	assert.Equal(t, "https://example.com:443", finding.Evidence)
	assert.Equal(t, "nuclei", finding.SourceTool)
	assert.Equal(t, 80.0, finding.Confidence)
	assert.NotEmpty(t, finding.References)
}

func TestNucleiCVEExtraction(t *testing.T) {
	event := &output.ResultEvent{
		TemplateID: "CVE-2021-44228",
		Matched:    "http://target:8080",
		Info: nucleimodel.Info{
			Name: "Apache Log4j RCE",
			SeverityHolder: severity.Holder{
				Severity: severity.Critical,
			},
			Classification: &nucleimodel.Classification{
				CVEID:     stringslice.New("CVE-2021-44228"),
				CVSSScore: 10.0,
			},
		},
	}

	finding := mapNucleiEvent(event)

	assert.Equal(t, "CVE-2021-44228", finding.CVE)
	assert.Equal(t, 10.0, finding.CVSS)
	assert.Equal(t, model.SeverityCritical, finding.Severity)
}

func TestNucleiAvailable(t *testing.T) {
	n := NewNucleiTool()
	assert.True(t, n.Available(), "nuclei library tool is always available")
	assert.Equal(t, ToolKindLibrary, n.Kind())
}

func TestNucleiSeverityMapping(t *testing.T) {
	tests := []struct {
		input    string
		expected model.Severity
	}{
		{"critical", model.SeverityCritical},
		{"high", model.SeverityHigh},
		{"medium", model.SeverityMedium},
		{"low", model.SeverityLow},
		{"info", model.SeverityInfo},
		{"unknown", model.SeverityInfo},
		{"CRITICAL", model.SeverityCritical},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.expected, mapNucleiSeverity(tc.input))
		})
	}
}
