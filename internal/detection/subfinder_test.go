package detection

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	subfinderRunner "github.com/projectdiscovery/subfinder/v2/pkg/runner"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// fakeSubfinderEnumerator captures the options it was constructed with
// and lets the test drive ctx-cancellation explicitly.
type fakeSubfinderEnumerator struct {
	opts        *subfinderRunner.Options
	enumerateFn func(ctx context.Context, domain string) (map[string]map[string]struct{}, error)
}

func (f *fakeSubfinderEnumerator) EnumerateSingleDomainWithCtx(ctx context.Context, domain string, _ []io.Writer) (map[string]map[string]struct{}, error) {
	if f.enumerateFn != nil {
		return f.enumerateFn(ctx, domain)
	}
	return map[string]map[string]struct{}{}, nil
}

func swapSubfinderEnumerator(t *testing.T, fn subfinderEnumeratorFactory) {
	t.Helper()
	prev := subfinderEnumeratorOverride
	subfinderEnumeratorOverride = fn
	t.Cleanup(func() { subfinderEnumeratorOverride = prev })
}

func TestResolveSubfinderParams_DefaultsAllSourcesWhenEmpty(t *testing.T) {
	got := resolveSubfinderParams(RunOptions{})
	assert.True(t, got.AllSources, "empty opts must default to AllSources=true (pre-1.2c behavior)")
}

func TestResolveSubfinderParams_ExplicitSourcesDisablesAll(t *testing.T) {
	got := resolveSubfinderParams(RunOptions{
		SubfinderParams: &model.SubfinderParams{Sources: []string{"crtsh"}},
	})
	assert.Equal(t, []string{"crtsh"}, got.Sources)
	assert.False(t, got.AllSources, "explicit Sources without AllSources must keep AllSources=false")
}

func TestSubfinder_ParamsPropagation_Sources(t *testing.T) {
	var captured *fakeSubfinderEnumerator
	swapSubfinderEnumerator(t, func(opts *subfinderRunner.Options) (subfinderEnumerator, error) {
		captured = &fakeSubfinderEnumerator{opts: opts}
		return captured, nil
	})

	s := NewSubfinderTool()
	_, err := s.Run(context.Background(), []string{"example.com"}, RunOptions{
		SubfinderParams: &model.SubfinderParams{Sources: []string{"crtsh", "virustotal"}},
	})
	require.NoError(t, err)
	require.NotNil(t, captured)
	assert.Equal(t, []string{"crtsh", "virustotal"}, []string(captured.opts.Sources),
		"params.Sources must reach the SDK Options.Sources field")
}

func TestSubfinder_ParamsPropagation_AllSources(t *testing.T) {
	var captured *fakeSubfinderEnumerator
	swapSubfinderEnumerator(t, func(opts *subfinderRunner.Options) (subfinderEnumerator, error) {
		captured = &fakeSubfinderEnumerator{opts: opts}
		return captured, nil
	})

	s := NewSubfinderTool()
	_, err := s.Run(context.Background(), []string{"example.com"}, RunOptions{
		SubfinderParams: &model.SubfinderParams{AllSources: true},
	})
	require.NoError(t, err)
	require.NotNil(t, captured)
	assert.True(t, captured.opts.All, "params.AllSources must reach the SDK Options.All field")
}

func TestSubfinder_CtxCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	swapSubfinderEnumerator(t, func(opts *subfinderRunner.Options) (subfinderEnumerator, error) {
		return &fakeSubfinderEnumerator{
			enumerateFn: func(c context.Context, _ string) (map[string]map[string]struct{}, error) {
				<-c.Done()
				return nil, c.Err()
			},
		}, nil
	})

	s := NewSubfinderTool()
	done := make(chan error, 1)
	go func() {
		_, err := s.Run(ctx, []string{"example.com"}, RunOptions{})
		done <- err
	}()
	time.AfterFunc(50*time.Millisecond, cancel)

	select {
	case err := <-done:
		// Subfinder swallows per-domain errors into the stderr accumulator
		// rather than propagating; assert it returned promptly without
		// requiring a typed ctx error.
		_ = err
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return within 500ms after ctx cancel")
	}
	require.True(t, errors.Is(ctx.Err(), context.Canceled))
}

func TestSubfinderOutputParsing(t *testing.T) {
	results, err := ParseSubfinderFile("testdata/subfinder_sample.txt")
	require.NoError(t, err)

	assert.Len(t, results, 5)
	assert.Contains(t, results, "www.example.com")
	assert.Contains(t, results, "mail.example.com")
	assert.Contains(t, results, "api.example.com")
	assert.Contains(t, results, "cdn.example.com")
	assert.Contains(t, results, "blog.example.com")
}

func TestSubfinderOutputDedup(t *testing.T) {
	data := []byte("www.example.com\nWWW.EXAMPLE.COM\nwww.example.com\napi.example.com\n")
	results := ParseSubfinderOutput(data)

	// Should be deduplicated and lowercased
	assert.Len(t, results, 2)
	assert.Contains(t, results, "www.example.com")
	assert.Contains(t, results, "api.example.com")
}

// TestSubfinderAvailable documents the SDK-embedded contract: since the
// tool no longer shells out to a binary, Available() is a constant true.
// Preserving the test ensures nobody silently regresses this by bringing
// back a binary dependency.
func TestSubfinderAvailable(t *testing.T) {
	s := NewSubfinderTool()
	assert.True(t, s.Available(),
		"subfinder is SDK-embedded (ToolKindLibrary); Available must be unconditionally true")
	assert.Equal(t, ToolKindLibrary, s.Kind(),
		"subfinder was migrated from a subprocess binary to the SDK — ToolKind must reflect that")
}

// TestSubfinderKindIsLibrary pins the contract at the Kind level too so a
// future change that flips Kind back to Native without updating Available
// also fails.
func TestSubfinderKindIsLibrary(t *testing.T) {
	s := NewSubfinderTool()
	assert.Equal(t, ToolKindLibrary, s.Kind())
}
