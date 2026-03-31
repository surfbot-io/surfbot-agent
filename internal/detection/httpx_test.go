package detection

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
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
	// Create a self-signed cert for the test server
	tlsCert := generateSelfSignedCert(t)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "nginx/1.25.0")
		w.WriteHeader(200)
		fmt.Fprint(w, `<html><head><title>Test Page</title></head><body>Hello</body></html>`)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	srv.StartTLS()
	defer srv.Close()

	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	// Test the probeURL function directly to avoid URL construction complexity
	client := srv.Client()
	client.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true

	targetURL := fmt.Sprintf("https://127.0.0.1:%s", port)
	pr := probeURL(context.Background(), client, targetURL)
	require.NotNil(t, pr, "expected probe to succeed for %s", targetURL)

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

func generateSelfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}
}
