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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestHTTPXVhostMismatch asserts that a response whose effective host does not
// match the expected hostname is dropped and not persisted.
func TestHTTPXVhostMismatch(t *testing.T) {
	// Multi-vhost server: body reflects the Host header.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Server refuses the expected vhost by redirecting to an unrelated host.
		// Simplest: just echo the Host back — the test expects mismatch via
		// hostname+cert, and the observed URL hostname is the IP (no redirect).
		fmt.Fprintf(w, "served host=%s", r.Host)
	}))
	defer srv.Close()

	ip, port := mustSplit(t, srv.Listener.Addr().String())

	// Capture mismatch log
	var logBuf bytes.Buffer
	orig := vhostMismatchLog
	vhostMismatchLog = &logBuf
	t.Cleanup(func() { vhostMismatchLog = orig })

	// The server is reached via IP. resp.Request.URL.Hostname() will be the
	// IP. Expected is "intended.test". No TLS, so no cert to rescue the match.
	// Result: mismatch → drop.
	target := probeTarget{
		URL:          fmt.Sprintf("http://%s:%s", ip, port),
		ExpectedHost: "intended.test",
		IP:           ip,
		Port:         atoi(port),
	}
	pr, dropped := probeURL(context.Background(), srv.Client(), target)
	assert.Nil(t, pr, "mismatched response must not return a probeResult")
	assert.True(t, dropped, "must be reported as a drop")

	// Structured log format (spec: key=value)
	line := logBuf.String()
	for _, want := range []string{
		"reason=vhost_mismatch",
		"expected_host=intended.test",
		"observed_host=" + ip,
		"ip=" + ip,
		"port=" + port,
		"status=200",
	} {
		assert.Contains(t, line, want, "log line %q missing %q", line, want)
	}
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
// ToolRun.Config map as vhost_mismatch_drops.
func TestHTTPXRunMismatchCounter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	// Silence the mismatch log in this test
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
