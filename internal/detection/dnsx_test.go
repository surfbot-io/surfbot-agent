package detection

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// fakeDnsxResolver records every LookupHost / LookupCNAME call so tests
// can assert the params drove the right RPCs and how many times each
// happened.
type fakeDnsxResolver struct {
	hostLookups     int32
	cnameLookups    int32
	hostFn          func(context.Context, string) ([]string, error)
	cnameFn         func(context.Context, string) (string, error)
	failHostNTimes  int32 // first N calls return err to exercise retry path
	hostFailureCall int32
}

func (f *fakeDnsxResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	atomic.AddInt32(&f.hostLookups, 1)
	if f.failHostNTimes > 0 {
		n := atomic.AddInt32(&f.hostFailureCall, 1)
		if n <= f.failHostNTimes {
			return nil, errors.New("transient")
		}
	}
	if f.hostFn != nil {
		return f.hostFn(ctx, host)
	}
	return []string{"203.0.113.1"}, nil
}

func (f *fakeDnsxResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	atomic.AddInt32(&f.cnameLookups, 1)
	if f.cnameFn != nil {
		return f.cnameFn(ctx, host)
	}
	return "", errors.New("no cname")
}

func swapDnsxResolver(t *testing.T, r dnsxResolver) {
	t.Helper()
	prev := dnsxResolverOverride
	dnsxResolverOverride = r
	t.Cleanup(func() { dnsxResolverOverride = prev })
}

func TestResolveDnsxParams_TypedOverridesWin(t *testing.T) {
	got := resolveDnsxParams(RunOptions{
		DnsxParams: &model.DnsxParams{RecordTypes: []string{"A"}, Retries: 5},
	})
	assert.Equal(t, []string{"A"}, got.RecordTypes)
	assert.Equal(t, 5, got.Retries)
}

func TestResolveDnsxParams_FallsBackToDefaults(t *testing.T) {
	got := resolveDnsxParams(RunOptions{})
	def := model.DefaultDnsxParams()
	assert.Equal(t, def.RecordTypes, got.RecordTypes)
	assert.Equal(t, def.Retries, got.Retries)
}

func TestDnsx_ParamsPropagation_RecordTypes(t *testing.T) {
	fake := &fakeDnsxResolver{}
	swapDnsxResolver(t, fake)

	d := NewDNSXTool()
	_, err := d.Run(context.Background(), []string{"example.com"}, RunOptions{
		DnsxParams: &model.DnsxParams{RecordTypes: []string{"CNAME"}, Retries: 1},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(0), atomic.LoadInt32(&fake.hostLookups),
		"RecordTypes=[CNAME] must skip A/AAAA host lookups")
	assert.Equal(t, int32(1), atomic.LoadInt32(&fake.cnameLookups),
		"RecordTypes=[CNAME] must still issue the CNAME lookup")
}

func TestDnsx_ParamsPropagation_Retries(t *testing.T) {
	fake := &fakeDnsxResolver{failHostNTimes: 2}
	swapDnsxResolver(t, fake)

	d := NewDNSXTool()
	_, err := d.Run(context.Background(), []string{"example.com"}, RunOptions{
		DnsxParams: &model.DnsxParams{RecordTypes: []string{"A"}, Retries: 3},
	})
	require.NoError(t, err)
	// 2 failures + 1 success = 3 calls.
	assert.Equal(t, int32(3), atomic.LoadInt32(&fake.hostLookups),
		"Retries=3 must let dnsx retry past 2 transient failures")
}

func TestDnsx_CtxCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := &fakeDnsxResolver{
		hostFn: func(c context.Context, _ string) ([]string, error) {
			<-c.Done()
			return nil, c.Err()
		},
	}
	swapDnsxResolver(t, fake)

	d := NewDNSXTool()
	done := make(chan struct{})
	go func() {
		_, _ = d.Run(ctx, []string{"example.com"}, RunOptions{
			DnsxParams: &model.DnsxParams{RecordTypes: []string{"A"}, Retries: 1},
		})
		close(done)
	}()
	time.AfterFunc(50*time.Millisecond, cancel)

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return within 500ms after ctx cancel")
	}
}

func TestDNSXResolvesPublicDomain(t *testing.T) {
	d := NewDNSXTool()
	result, err := d.Run(context.Background(), []string{"example.com"}, RunOptions{})
	require.NoError(t, err)

	// example.com should resolve to at least one IP
	assert.NotEmpty(t, result.Assets, "expected at least one resolved IP for example.com")

	// Verify we got IP assets
	for _, a := range result.Assets {
		assert.Contains(t, []string{"ipv4", "ipv6"}, string(a.Type))
		assert.NotEmpty(t, a.Value)
	}
}

func TestDNSXHandlesNXDOMAIN(t *testing.T) {
	d := NewDNSXTool()
	result, err := d.Run(context.Background(), []string{"nonexistent.invalid"}, RunOptions{})
	require.NoError(t, err)

	// NXDOMAIN should return empty, not error
	assert.Empty(t, result.Assets)
}

func TestDNSXConcurrency(t *testing.T) {
	d := NewDNSXTool()

	// Resolve multiple domains concurrently — test for race conditions
	domains := []string{
		"example.com",
		"example.org",
		"example.net",
		"google.com",
		"cloudflare.com",
		"nonexistent1.invalid",
		"nonexistent2.invalid",
		"nonexistent3.invalid",
		"github.com",
		"mozilla.org",
	}

	var wg sync.WaitGroup
	errors := make(chan error, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := d.Run(context.Background(), domains, RunOptions{RateLimit: 20})
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("unexpected error: %v", err)
	}
}
