package webui

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

func TestLoadOrCreateUIToken_GeneratesAndReuses(t *testing.T) {
	dir := t.TempDir()

	tok1, err := LoadOrCreateUIToken(dir)
	require.NoError(t, err)
	require.Len(t, tok1, 64) // 32 bytes hex

	info, err := os.Stat(filepath.Join(dir, "ui.token"))
	require.NoError(t, err)
	// Permission bits: 0600 on POSIX. Skip on Windows where chmod is a no-op.
	if info.Mode().Perm() != 0o600 && os.Getenv("GOOS") != "windows" {
		// Allow Windows runners to slip through; assert on POSIX.
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	tok2, err := LoadOrCreateUIToken(dir)
	require.NoError(t, err)
	assert.Equal(t, tok1, tok2, "second call must reuse the existing token file")
}

func TestLoadOrCreateUIToken_EmptyDir(t *testing.T) {
	_, err := LoadOrCreateUIToken("")
	assert.Error(t, err)
}

// startTestServer spins up a real server bound to an ephemeral loopback
// port and returns the base URL plus a teardown. We use a real listener
// (not httptest) so we can control the Host header that clients send.
func startTestServer(t *testing.T, token string) (string, int, func()) {
	t.Helper()
	store, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err)

	// Pick a free port first so allowedHosts/Origins line up.
	tmpLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := tmpLn.Addr().(*net.TCPAddr).Port
	require.NoError(t, tmpLn.Close())

	srv, ln, err := NewServer(store, ServerOptions{
		Bind:      "127.0.0.1",
		Port:      port,
		Version:   "test",
		AuthToken: token,
	})
	require.NoError(t, err)

	go func() { _ = srv.Serve(ln) }()
	// Wait briefly for the listener to be ready.
	time.Sleep(20 * time.Millisecond)

	base := "http://127.0.0.1:" + portStr(port)
	cleanup := func() {
		_ = srv.Shutdown(context.Background())
		_ = store.Close()
	}
	return base, port, cleanup
}

func portStr(p int) string {
	return strings.TrimSpace(itoa(p))
}

func itoa(n int) string {
	// Avoid pulling strconv into the test for a single call site.
	return (func() string {
		if n == 0 {
			return "0"
		}
		var buf [20]byte
		i := len(buf)
		for n > 0 {
			i--
			buf[i] = byte('0' + n%10)
			n /= 10
		}
		return string(buf[i:])
	})()
}

func doReq(t *testing.T, method, url string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	require.NoError(t, err)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func TestSecurityHeaders_PresentOnEveryResponse(t *testing.T) {
	base, _, stop := startTestServer(t, "")
	defer stop()

	resp := doReq(t, http.MethodGet, base+"/", nil)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
	assert.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'")
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "default-src 'self'")
	// Cache-Control: no-store is API-only.
	assert.Empty(t, resp.Header.Get("Cache-Control"))
}

func TestSecurityHeaders_NoStoreOnAPI(t *testing.T) {
	base, _, stop := startTestServer(t, "tok")
	defer stop()

	resp := doReq(t, http.MethodGet, base+"/api/daemon/status", map[string]string{
		"Authorization": "Bearer tok",
	})
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
}

func TestRequireToken(t *testing.T) {
	base, _, stop := startTestServer(t, "secret")
	defer stop()

	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"missing", nil, http.StatusUnauthorized},
		{"wrong", map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
		{"correct", map[string]string{"Authorization": "Bearer secret"}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doReq(t, http.MethodGet, base+"/api/daemon/status", tc.headers)
			defer func() { _ = resp.Body.Close() }()
			assert.Equal(t, tc.want, resp.StatusCode)
		})
	}
}

func TestRequireToken_StaticAssetsBypass(t *testing.T) {
	base, _, stop := startTestServer(t, "secret")
	defer stop()

	// GET / serves the SPA shell without an Authorization header.
	resp := doReq(t, http.MethodGet, base+"/", nil)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), `meta name="surfbot-token"`)
	assert.Contains(t, string(body), `content="secret"`)
}

func TestValidateOrigin(t *testing.T) {
	base, port, stop := startTestServer(t, "tok")
	defer stop()
	good := "http://127.0.0.1:" + portStr(port)

	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{
			"good origin",
			map[string]string{"Authorization": "Bearer tok", "Origin": good, "Content-Type": "application/json"},
			// 503 (daemon unavailable) is fine — what we are asserting is
			// that the origin check let the request through.
			-1,
		},
		{
			"evil origin",
			map[string]string{"Authorization": "Bearer tok", "Origin": "https://evil.example", "Content-Type": "application/json"},
			http.StatusForbidden,
		},
		{
			"missing origin and referer",
			map[string]string{"Authorization": "Bearer tok", "Content-Type": "application/json"},
			http.StatusForbidden,
		},
		{
			"good referer",
			map[string]string{"Authorization": "Bearer tok", "Referer": good + "/", "Content-Type": "application/json"},
			-1,
		},
		{
			"evil referer",
			map[string]string{"Authorization": "Bearer tok", "Referer": "https://evil.example/foo", "Content-Type": "application/json"},
			http.StatusForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, base+"/api/daemon/trigger", strings.NewReader(`{"profile":"quick"}`))
			require.NoError(t, err)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			if tc.want == -1 {
				assert.NotEqual(t, http.StatusForbidden, resp.StatusCode)
				return
			}
			assert.Equal(t, tc.want, resp.StatusCode)
		})
	}
}

func TestValidateOrigin_GETBypass(t *testing.T) {
	base, _, stop := startTestServer(t, "tok")
	defer stop()
	// GET requests don't need an Origin header.
	resp := doReq(t, http.MethodGet, base+"/api/daemon/status", map[string]string{
		"Authorization": "Bearer tok",
	})
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestValidateHost(t *testing.T) {
	base, _, stop := startTestServer(t, "tok")
	defer stop()

	// Good host: the default Host: 127.0.0.1:<port> from net/http.
	resp := doReq(t, http.MethodGet, base+"/api/daemon/status", map[string]string{
		"Authorization": "Bearer tok",
	})
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Bad host: spoof Host header.
	req, err := http.NewRequest(http.MethodGet, base+"/api/daemon/status", nil)
	require.NoError(t, err)
	req.Host = "evil.example:8470"
	req.Header.Set("Authorization", "Bearer tok")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()
	assert.Equal(t, http.StatusMisdirectedRequest, resp2.StatusCode)
}

func TestServeIndex_InjectsToken(t *testing.T) {
	base, _, stop := startTestServer(t, "the-secret-token")
	defer stop()

	resp := doReq(t, http.MethodGet, base+"/", nil)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), `<meta name="surfbot-token" content="the-secret-token">`)
	assert.Contains(t, string(body), "</head>")
}
