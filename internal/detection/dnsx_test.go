package detection

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
