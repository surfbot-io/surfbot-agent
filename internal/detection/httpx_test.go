package detection

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// fakeRoundTripper captures every request and returns a canned response.
type fakeRoundTripper struct {
	mu       sync.Mutex
	requests []*http.Request
	respond  func(*http.Request) (*http.Response, error)
}

func (f *fakeRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	respond := f.respond
	f.mu.Unlock()
	if respond != nil {
		return respond(r)
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Header:     http.Header{},
		Request:    r,
	}, nil
}

func swapHttpxTransport(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	prev := httpxTransportOverride
	httpxTransportOverride = rt
	t.Cleanup(func() { httpxTransportOverride = prev })
}

func TestResolveHttpxParams_TypedOverridesWin(t *testing.T) {
	got := resolveHttpxParams(RunOptions{
		HttpxParams: &model.HttpxParams{
			Threads: 25,
			Probes:  []string{"https"},
			Timeout: 3 * time.Second,
		},
	})
	assert.Equal(t, 25, got.Threads)
	assert.Equal(t, []string{"https"}, got.Probes)
	assert.Equal(t, 3*time.Second, got.Timeout)
}

func TestResolveHttpxParams_FallsBackToDefaults(t *testing.T) {
	got := resolveHttpxParams(RunOptions{})
	def := model.DefaultHttpxParams()
	assert.Equal(t, def.Threads, got.Threads)
	assert.Equal(t, def.Probes, got.Probes)
	assert.Equal(t, def.Timeout, got.Timeout)
}

func TestHttpx_ParamsPropagation_Threads(t *testing.T) {
	var inflight, peak int32
	rt := &fakeRoundTripper{
		respond: func(r *http.Request) (*http.Response, error) {
			cur := atomic.AddInt32(&inflight, 1)
			defer atomic.AddInt32(&inflight, -1)
			for {
				old := atomic.LoadInt32(&peak)
				if cur <= old || atomic.CompareAndSwapInt32(&peak, old, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     http.Header{},
				Request:    r,
			}, nil
		},
	}
	swapHttpxTransport(t, rt)

	h := NewHTTPXTool()
	// 16 inputs × http+https = 32 probes. Threads=4 caps in-flight at 4.
	inputs := make([]string, 16)
	for i := range inputs {
		inputs[i] = fmt.Sprintf("host%d:80", i)
	}
	_, err := h.Run(context.Background(), inputs, RunOptions{
		HttpxParams: &model.HttpxParams{Threads: 4, Probes: []string{"http", "https"}, Timeout: 5 * time.Second},
	})
	require.NoError(t, err)
	observed := atomic.LoadInt32(&peak)
	assert.LessOrEqual(t, observed, int32(4),
		"peak in-flight requests must respect params.Threads (got %d)", observed)
}

func TestHttpx_ParamsPropagation_Probes(t *testing.T) {
	rt := &fakeRoundTripper{}
	swapHttpxTransport(t, rt)

	h := NewHTTPXTool()
	_, err := h.Run(context.Background(), []string{"example.com:8080"}, RunOptions{
		HttpxParams: &model.HttpxParams{Threads: 4, Probes: []string{"https"}, Timeout: 5 * time.Second},
	})
	require.NoError(t, err)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, r := range rt.requests {
		assert.Equal(t, "https", r.URL.Scheme,
			"probes=[https] must skip http requests; saw %s", r.URL)
	}
}

func TestHttpx_CtxCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rt := &fakeRoundTripper{
		respond: func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		},
	}
	swapHttpxTransport(t, rt)

	h := NewHTTPXTool()
	done := make(chan struct{})
	go func() {
		_, _ = h.Run(ctx, []string{"example.com:8080"}, RunOptions{
			HttpxParams: &model.HttpxParams{Threads: 4, Probes: []string{"http"}, Timeout: 5 * time.Second},
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

func TestHTTPXProbeHTTPS(t *testing.T) {
	tlsCert := generateSelfSignedCert(t, "localhost", "127.0.0.1")

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "nginx/1.25.0")
		w.WriteHeader(200)
		fmt.Fprint(w, `<html><head><title>Test Page</title></head><body>Hello</body></html>`)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	srv.StartTLS()
	defer srv.Close()

	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	client := srv.Client()
	client.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true

	target := probeTarget{URL: fmt.Sprintf("https://127.0.0.1:%s", port)}
	pr, dropped := probeURL(context.Background(), client, target)
	require.NotNil(t, pr, "expected probe to succeed for %s", target.URL)
	assert.False(t, dropped)

	assert.Equal(t, 200, pr.StatusCode)
	assert.Equal(t, "Test Page", pr.Title)
	assert.Contains(t, pr.Server, "nginx")
	assert.Contains(t, pr.TechList, "nginx")
}

func TestTechDetection(t *testing.T) {
	tests := []struct {
		name     string
		headers  http.Header
		body     string
		expected []string
	}{
		{
			name:     "nginx server header",
			headers:  http.Header{"Server": {"nginx/1.25.0"}},
			body:     "",
			expected: []string{"nginx"},
		},
		{
			name:     "apache server header",
			headers:  http.Header{"Server": {"Apache/2.4.57"}},
			body:     "",
			expected: []string{"Apache"},
		},
		{
			name:     "php powered by",
			headers:  http.Header{"X-Powered-By": {"PHP/8.2.0"}},
			body:     "",
			expected: []string{"PHP"},
		},
		{
			name:     "wordpress body only (no header, no match due to AND logic)",
			headers:  http.Header{},
			body:     `<link rel="stylesheet" href="/wp-content/themes/theme/style.css">`,
			expected: nil, // WordPress rule requires both header AND body
		},
		{
			name:     "react body patterns",
			headers:  http.Header{},
			body:     `<script id="__NEXT_DATA__" type="application/json">{"props":{}}</script>`,
			expected: []string{"React"},
		},
		{
			name:     "cloudflare header",
			headers:  http.Header{"Server": {"cloudflare"}},
			body:     "",
			expected: []string{"Cloudflare"},
		},
		{
			name:     "no match",
			headers:  http.Header{"Server": {"CustomServer/1.0"}},
			body:     "<html><body>plain page</body></html>",
			expected: nil,
		},
		{
			name:     "wordpress both header and body",
			headers:  http.Header{"X-Powered-By": {"WordPress"}},
			body:     `<link href="/wp-content/themes/theme/style.css">`,
			expected: []string{"WordPress"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			detected := DetectTechnologies(tc.headers, tc.body)
			if tc.expected == nil {
				assert.Empty(t, detected)
			} else {
				for _, exp := range tc.expected {
					assert.Contains(t, detected, exp)
				}
			}
		})
	}
}

func TestTitleExtraction(t *testing.T) {
	tests := []struct {
		html     string
		expected string
	}{
		{`<title>Hello World</title>`, "Hello World"},
		{`<TITLE>Upper Case</TITLE>`, "Upper Case"},
		{`<title lang="en">With Attr</title>`, "With Attr"},
		{`<title>  Spaces  </title>`, "Spaces"},
		{`<html><body>No title here</body></html>`, ""},
		{`<title></title>`, ""},
		{`<title>First</title><title>Second</title>`, "First"},
	}

	for _, tc := range tests {
		result := ExtractTitle(tc.html)
		assert.Equal(t, tc.expected, result, "html: %q", tc.html)
	}
}

// TestBuildProbeURLs covers the input format widening for SUR-242.
func TestBuildProbeURLs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []probeTarget
	}{
		{
			name:  "legacy ip:port/tcp (IP-pure)",
			input: "1.2.3.4:443/tcp",
			want: []probeTarget{
				{URL: "https://1.2.3.4:443", ExpectedHost: "", IP: "1.2.3.4", Port: 443},
			},
		},
		{
			name:  "enriched hostname|ip:port/tcp",
			input: "example.com|1.2.3.4:443/tcp",
			want: []probeTarget{
				{URL: "https://1.2.3.4:443", ExpectedHost: "example.com", IP: "1.2.3.4", Port: 443},
			},
		},
		{
			name:  "non-default port produces both schemes",
			input: "example.com|1.2.3.4:8080/tcp",
			want: []probeTarget{
				{URL: "http://1.2.3.4:8080", ExpectedHost: "example.com", IP: "1.2.3.4", Port: 8080},
				{URL: "https://1.2.3.4:8080", ExpectedHost: "example.com", IP: "1.2.3.4", Port: 8080},
			},
		},
		{
			name:  "port 80 emits only http",
			input: "example.com|1.2.3.4:80/tcp",
			want: []probeTarget{
				{URL: "http://1.2.3.4:80", ExpectedHost: "example.com", IP: "1.2.3.4", Port: 80},
			},
		},
		{
			name:  "bare hostname scopes to itself",
			input: "example.com",
			want: []probeTarget{
				{URL: "http://example.com", ExpectedHost: "example.com"},
				{URL: "https://example.com", ExpectedHost: "example.com"},
			},
		},
		{
			name:  "bare IP stays IP-pure",
			input: "1.2.3.4",
			want: []probeTarget{
				{URL: "http://1.2.3.4", ExpectedHost: ""},
				{URL: "https://1.2.3.4", ExpectedHost: ""},
			},
		},
		{
			// naabu emits hostname:port/tcp assets when it port-scans a
			// hostname. Without self-scoping here, the probe would be
			// IP-pure and off-site redirects would silently persist.
			name:  "hostname:port/tcp self-scopes",
			input: "www.example.com:443/tcp",
			want: []probeTarget{
				{URL: "https://www.example.com:443", ExpectedHost: "www.example.com", IP: "", Port: 443},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildProbeURLs([]string{tc.input})
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestHTTPXHostHeaderSent asserts that an enriched input causes the probe to
// issue the request with Host set to the declared hostname, so a multi-vhost
// server returns the intended vhost. Uses HTTPS + SAN cert so the scope check
// passes via cert coverage despite the URL targeting the IP.
func TestHTTPXHostHeaderSent(t *testing.T) {
	tlsCert := generateSelfSignedCertWithSANs(t,
		[]string{"intended.test"},
		[]net.IP{net.ParseIP("127.0.0.1")},
	)

	var seenHost string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
		fmt.Fprintf(w, "<title>%s</title>", r.Host)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	srv.StartTLS()
	defer srv.Close()

	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	client := srv.Client()
	client.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true

	target := probeTarget{
		URL:          fmt.Sprintf("https://127.0.0.1:%s", port),
		ExpectedHost: "intended.test",
		IP:           "127.0.0.1",
		Port:         atoi(port),
	}

	pr, dropped := probeURL(context.Background(), client, target)
	require.NotNil(t, pr)
	assert.False(t, dropped)
	assert.Equal(t, "intended.test", seenHost, "server must see the expected Host header")
	// URL recorded as the hostname, not the IP (so downstream assets are scoped)
	assert.Contains(t, pr.URL, "intended.test")
	assert.NotContains(t, pr.URL, "127.0.0.1")
}

// TestHTTPXVhostMismatchTLS models the original bwapp → mmebvba bug: the
// shared-IP server's cert covers a different domain, so the response is
// attributable to that other domain, not the target we asked for. The scope
// check must drop and log.
func TestHTTPXVhostMismatchTLS(t *testing.T) {
	// Cert only covers out-of-scope.test — not the intended.test we'll ask for.
	tlsCert := generateSelfSignedCertWithSANs(t,
		[]string{"out-of-scope.test"},
		[]net.IP{net.ParseIP("127.0.0.1")},
	)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "served host=%s", r.Host)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	srv.StartTLS()
	defer srv.Close()

	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	client := srv.Client()
	client.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true

	var logBuf bytes.Buffer
	orig := vhostMismatchLog
	vhostMismatchLog = &logBuf
	t.Cleanup(func() { vhostMismatchLog = orig })

	target := probeTarget{
		URL:          fmt.Sprintf("https://127.0.0.1:%s", port),
		ExpectedHost: "intended.test",
		IP:           "127.0.0.1",
		Port:         atoi(port),
	}
	pr, dropped := probeURL(context.Background(), client, target)
	assert.Nil(t, pr, "mismatched response must not return a probeResult")
	assert.True(t, dropped, "must be reported as a drop")

	line := logBuf.String()
	for _, want := range []string{
		"reason=vhost_mismatch",
		"expected_host=intended.test",
		"observed_host=127.0.0.1",
		"ip=127.0.0.1",
		"port=" + port,
		"status=200",
	} {
		assert.Contains(t, line, want, "log line %q missing %q", line, want)
	}
}

// TestHTTPXRedirectOffsiteDrops covers SUR-242 problem 2: a probe that is
// redirected off-site (e.g. naabu-derived hostname:port input where the server
// 302s to a different default vhost) must be dropped, not persisted under the
// original target. Uses "localhost" (which still resolves to 127.0.0.1 so the
// offsite server is reachable) to make the URL hostname after redirect clearly
// differ from the expected hostname.
func TestHTTPXRedirectOffsiteDrops(t *testing.T) {
	offsite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<title>out-of-scope</title>")
	}))
	defer offsite.Close()

	// Swap 127.0.0.1 for localhost in the redirect target so resp.Request.URL
	// carries a distinct hostname after the client follows the redirect.
	redirectTarget := strings.Replace(offsite.URL, "127.0.0.1", "localhost", 1) + "/landing"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget, http.StatusFound)
	}))
	defer srv.Close()

	ip, port := mustSplit(t, srv.Listener.Addr().String())

	orig := vhostMismatchLog
	vhostMismatchLog = io.Discard
	t.Cleanup(func() { vhostMismatchLog = orig })

	target := probeTarget{
		URL:          fmt.Sprintf("http://%s:%s", ip, port),
		ExpectedHost: "intended.test",
		IP:           ip,
		Port:         atoi(port),
	}
	pr, dropped := probeURL(context.Background(), srv.Client(), target)
	assert.Nil(t, pr, "redirect off-site must be dropped")
	assert.True(t, dropped)
}

// TestHTTPXPlaintextHTTPTrustsHost is the counterpart to the above: plaintext
// HTTP dialed by IP without any redirect has no cryptographic evidence the
// server served a different vhost, but it also has no evidence it did — and a
// redirect-to-default would already have tripped rule 1. Over-rejecting these
// hides legitimate HTTP services on shared IPs, so scopeMatches accepts them.
// See SUR-242 QA feedback, Problem 1.
func TestHTTPXPlaintextHTTPTrustsHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Server honors Host header — body varies so we can assert we got the
		// scoped probe's response, not a default.
		fmt.Fprintf(w, "<title>vhost=%s</title>", r.Host)
	}))
	defer srv.Close()

	ip, port := mustSplit(t, srv.Listener.Addr().String())

	target := probeTarget{
		URL:          fmt.Sprintf("http://%s:%s", ip, port),
		ExpectedHost: "intended.test",
		IP:           ip,
		Port:         atoi(port),
	}
	pr, dropped := probeURL(context.Background(), srv.Client(), target)
	require.NotNil(t, pr, "plaintext HTTP with no redirect must pass scope check")
	assert.False(t, dropped)
	assert.Contains(t, pr.Title, "vhost=intended.test", "Host header must have reached the server")
	// Recorded URL uses the hostname, not the IP — asset attribution stays scoped.
	assert.Contains(t, pr.URL, "intended.test")
	assert.NotContains(t, pr.URL, ip)
}

// TestHTTPXIPPureNoCheck asserts R4: bare IP targets are never scope-checked.
// Whatever the server returns is persisted under the IP target.
func TestHTTPXIPPureNoCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<title>default vhost</title>")
	}))
	defer srv.Close()

	ip, port := mustSplit(t, srv.Listener.Addr().String())

	target := probeTarget{
		URL:          fmt.Sprintf("http://%s:%s", ip, port),
		ExpectedHost: "", // IP-pure
		IP:           ip,
		Port:         atoi(port),
	}
	pr, dropped := probeURL(context.Background(), srv.Client(), target)
	require.NotNil(t, pr)
	assert.False(t, dropped)
	assert.Equal(t, "default vhost", pr.Title)
	// URL keeps the IP (no hostname to rewrite to)
	assert.Contains(t, pr.URL, ip)
}

// TestHTTPXCertSANLenience asserts that wildcard cert SAN coverage counts as a
// scope match even when the URL hostname (IP) differs from ExpectedHost.
func TestHTTPXCertSANLenience(t *testing.T) {
	tlsCert := generateSelfSignedCertWithSANs(t, []string{"*.example.com"}, []net.IP{net.ParseIP("127.0.0.1")})

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	srv.StartTLS()
	defer srv.Close()

	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	client := srv.Client()
	client.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true

	target := probeTarget{
		URL:          fmt.Sprintf("https://127.0.0.1:%s", port),
		ExpectedHost: "api.example.com",
		IP:           "127.0.0.1",
		Port:         atoi(port),
	}
	pr, dropped := probeURL(context.Background(), client, target)
	require.NotNil(t, pr)
	assert.False(t, dropped, "cert SAN *.example.com should cover api.example.com")
}

// TestHTTPXRunMismatchCounter asserts that drops are accumulated on the
// ToolRun.Config map as vhost_mismatch_drops. Uses an off-site redirect to
// trigger the check (plaintext-HTTP-no-redirect is intentionally trusted; see
// TestHTTPXPlaintextHTTPTrustsHost).
func TestHTTPXRunMismatchCounter(t *testing.T) {
	offsite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "out")
	}))
	defer offsite.Close()

	redirectTarget := strings.Replace(offsite.URL, "127.0.0.1", "localhost", 1) + "/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget, http.StatusFound)
	}))
	defer srv.Close()

	orig := vhostMismatchLog
	vhostMismatchLog = io.Discard
	t.Cleanup(func() { vhostMismatchLog = orig })

	ip, port := mustSplit(t, srv.Listener.Addr().String())
	tool := NewHTTPXTool()
	res, err := tool.Run(context.Background(), []string{
		"out-of-scope.test|" + ip + ":" + port + "/tcp",
	}, RunOptions{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Empty(t, res.Assets, "mismatched probes must not produce assets")
	drops, _ := res.ToolRun.Config["vhost_mismatch_drops"].(int)
	assert.GreaterOrEqual(t, drops, 1, "expected at least one recorded drop")
}

func generateSelfSignedCert(t *testing.T, cn string, ips ...string) tls.Certificate {
	t.Helper()
	ipAddrs := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		ipAddrs = append(ipAddrs, net.ParseIP(ip))
	}
	return generateSelfSignedCertRaw(t, cn, nil, ipAddrs)
}

func generateSelfSignedCertWithSANs(t *testing.T, dnsSANs []string, ips []net.IP) tls.Certificate {
	t.Helper()
	return generateSelfSignedCertRaw(t, "test", dnsSANs, ips)
}

func generateSelfSignedCertRaw(t *testing.T, cn string, dnsSANs []string, ips []net.IP) tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  ips,
		DNSNames:     dnsSANs,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}
}

func mustSplit(t *testing.T, addr string) (string, string) {
	t.Helper()
	ip, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	return ip, port
}

func atoi(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}
