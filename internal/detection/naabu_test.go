package detection

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func swapNaabuDialer(t *testing.T, fn dialFunc) {
	t.Helper()
	prev := defaultDialer
	defaultDialer = fn
	t.Cleanup(func() { defaultDialer = prev })
}

func TestResolveNaabuParams_TypedOverridesWin(t *testing.T) {
	got := resolveNaabuParams(RunOptions{
		NaabuParams: &model.NaabuParams{Ports: "22,80", Rate: 5, Retries: 2},
	})
	assert.Equal(t, "22,80", got.Ports)
	assert.Equal(t, 5, got.Rate)
	assert.Equal(t, 2, got.Retries)
}

func TestResolveNaabuParams_FallsBackToDefaults(t *testing.T) {
	got := resolveNaabuParams(RunOptions{})
	def := model.DefaultNaabuParams()
	assert.Equal(t, def.Ports, got.Ports)
	assert.Equal(t, def.Rate, got.Rate)
}

func TestNaabu_ParamsPropagation_Ports(t *testing.T) {
	var seen sync.Map
	dial := func(network, addr string, _ time.Duration) (net.Conn, error) {
		seen.Store(addr, true)
		return nil, errors.New("dial tcp: connect: connection refused")
	}
	swapNaabuDialer(t, dial)

	n := NewNaabuTool()
	_, err := n.Run(context.Background(), []string{"1.2.3.4"}, RunOptions{
		NaabuParams: &model.NaabuParams{Ports: "22,80", Rate: 4, BannerGrab: true},
	})
	require.NoError(t, err)
	_, hit22 := seen.Load("1.2.3.4:22")
	_, hit80 := seen.Load("1.2.3.4:80")
	_, hit443 := seen.Load("1.2.3.4:443")
	assert.True(t, hit22, "dialer must have been called for port 22 from params.Ports")
	assert.True(t, hit80, "dialer must have been called for port 80 from params.Ports")
	assert.False(t, hit443, "port 443 must not be probed when Ports=\"22,80\"")
}

func TestNaabu_ParamsPropagation_Rate(t *testing.T) {
	var inflight, peak int32
	dial := func(network, addr string, _ time.Duration) (net.Conn, error) {
		cur := atomic.AddInt32(&inflight, 1)
		defer atomic.AddInt32(&inflight, -1)
		// Track high-water mark of concurrent dials.
		for {
			old := atomic.LoadInt32(&peak)
			if cur <= old || atomic.CompareAndSwapInt32(&peak, old, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		return nil, errors.New("connection refused")
	}
	swapNaabuDialer(t, dial)

	n := NewNaabuTool()
	// 16 ports, Rate=4 → effective concurrency capped at 4.
	_, err := n.Run(context.Background(), []string{"1.2.3.4"}, RunOptions{
		NaabuParams: &model.NaabuParams{
			Ports:      "1-16",
			Rate:       4,
			BannerGrab: true,
		},
	})
	require.NoError(t, err)
	observed := atomic.LoadInt32(&peak)
	assert.LessOrEqual(t, observed, int32(4),
		"peak concurrency must be ≤ params.Rate (got %d)", observed)
}

func TestNaabu_CtxCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dial := func(network, addr string, _ time.Duration) (net.Conn, error) {
		// Block until ctx fires so cancellation propagates promptly through
		// every in-flight dial. Without this, naabu's wg.Wait() would
		// stall on goroutines blocked in net.DialTimeout.
		<-ctx.Done()
		return nil, ctx.Err()
	}
	swapNaabuDialer(t, dial)

	n := NewNaabuTool()
	done := make(chan struct{})
	go func() {
		_, _ = n.Run(ctx, []string{"1.2.3.4"}, RunOptions{
			NaabuParams: &model.NaabuParams{Ports: "1-50", Rate: 10, BannerGrab: false},
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

func TestPortParsing(t *testing.T) {
	tests := []struct {
		input    string
		expected []int
		hasError bool
	}{
		{"80,443", []int{80, 443}, false},
		{"1-5", []int{1, 2, 3, 4, 5}, false},
		{"top100", Top100Ports, false},
		{"", Top100Ports, false},
		{"80, 443, 8080", []int{80, 443, 8080}, false},
		{"invalid", nil, true},
		{"0", nil, true},
		{"65536", nil, true},
		{"100-50", nil, true},
	}

	for _, tc := range tests {
		ports, err := ParsePorts(tc.input)
		if tc.hasError {
			assert.Error(t, err, "input: %q", tc.input)
		} else {
			require.NoError(t, err, "input: %q", tc.input)
			assert.Equal(t, tc.expected, ports, "input: %q", tc.input)
		}
	}
}

func TestNaabuScanLocalhost(t *testing.T) {
	// Start a TCP test server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close() //nolint:errcheck

	// Accept connections in background
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())

	n := NewNaabuTool()
	result, err := n.Run(context.Background(), []string{"127.0.0.1"}, RunOptions{
		ExtraArgs: map[string]string{"ports": portStr},
	})
	require.NoError(t, err)

	// Should find our test port open
	require.Len(t, result.Assets, 1)
	assert.Equal(t, fmt.Sprintf("127.0.0.1:%s/tcp", portStr), result.Assets[0].Value)
}

func TestNaabuClosedPort(t *testing.T) {
	n := NewNaabuTool()
	// Port 1 is almost certainly closed on localhost
	result, err := n.Run(context.Background(), []string{"127.0.0.1"}, RunOptions{
		ExtraArgs: map[string]string{"ports": "1", "timeout": "1"},
	})
	require.NoError(t, err)
	assert.Empty(t, result.Assets)
}

// --- SPEC-QA2 R1: default concurrency ---

func TestDefaultConcurrencyIs20(t *testing.T) {
	assert.Equal(t, 20, defaultConcurrency,
		"SPEC-QA2 R1: default must be 20 (was 100 pre-fix)")
}

// --- SPEC-QA2 R2: retry-on-timeout ---

// fakeTimeoutErr satisfies net.Error with Timeout()=true.
type fakeTimeoutErr struct{}

func (fakeTimeoutErr) Error() string   { return "i/o timeout" }
func (fakeTimeoutErr) Timeout() bool   { return true }
func (fakeTimeoutErr) Temporary() bool { return true }

type fakeConn struct {
	readBytes []byte
	readPos   int
}

func (f *fakeConn) Read(b []byte) (int, error) {
	if f.readPos >= len(f.readBytes) {
		return 0, io.EOF
	}
	n := copy(b, f.readBytes[f.readPos:])
	f.readPos += n
	return n, nil
}
func (f *fakeConn) Write(b []byte) (int, error)        { return len(b), nil }
func (f *fakeConn) Close() error                       { return nil }
func (f *fakeConn) LocalAddr() net.Addr                { return nil }
func (f *fakeConn) RemoteAddr() net.Addr               { return nil }
func (f *fakeConn) SetDeadline(t time.Time) error      { return nil }
func (f *fakeConn) SetReadDeadline(t time.Time) error  { return nil }
func (f *fakeConn) SetWriteDeadline(t time.Time) error { return nil }

func TestRetryOnTimeoutThenSuccess(t *testing.T) {
	var attempts int32
	dial := func(network, addr string, timeout time.Duration) (net.Conn, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			return nil, fakeTimeoutErr{}
		}
		return &fakeConn{readBytes: []byte("SSH-2.0-test\r\n")}, nil
	}

	n := &NaabuTool{}
	br := newBreaker(20)
	results, _, _ := n.runScan(context.Background(), []string{"1.2.3.4"}, []int{22}, time.Second, 20, 4, false, br, dial)
	require.Len(t, results, 1)
	assert.Equal(t, "open", results[0].Status)
	assert.Contains(t, results[0].BannerPreview, "SSH-2.0")
	assert.Equal(t, int32(2), atomic.LoadInt32(&attempts), "one retry after the initial timeout")
}

func TestNoRetryOnRefused(t *testing.T) {
	var attempts int32
	dial := func(network, addr string, timeout time.Duration) (net.Conn, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, errors.New("dial tcp 1.2.3.4:1: connect: connection refused")
	}
	n := &NaabuTool{}
	br := newBreaker(20)
	results, _, _ := n.runScan(context.Background(), []string{"1.2.3.4"}, []int{1}, time.Second, 20, 4, false, br, dial)
	assert.Empty(t, results)
	assert.Equal(t, int32(1), atomic.LoadInt32(&attempts), "refused must not retry — it's a definitive closed port")
}

func TestTwoTimeoutsMeanClosed(t *testing.T) {
	var attempts int32
	dial := func(network, addr string, timeout time.Duration) (net.Conn, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, fakeTimeoutErr{}
	}
	n := &NaabuTool{}
	br := newBreaker(20)
	results, _, failures := n.runScan(context.Background(), []string{"1.2.3.4"}, []int{22}, time.Second, 20, 4, false, br, dial)
	assert.Empty(t, results)
	assert.Equal(t, int32(2), atomic.LoadInt32(&attempts), "original + one retry")
	assert.Equal(t, 1, failures)
}

// --- SPEC-QA2 R3: breaker-under-retry ---

func TestBreakerClampsUnderTimeoutBurst(t *testing.T) {
	// 80% of the first 50 calls are timeouts (after retry), 20% succeed.
	var attempts int32
	dial := func(network, addr string, timeout time.Duration) (net.Conn, error) {
		n := atomic.AddInt32(&attempts, 1)
		// The first N × 2 calls (original + retry) all time out; the rest
		// succeed on the first attempt.
		if n <= 80 {
			return nil, fakeTimeoutErr{}
		}
		return &fakeConn{readBytes: []byte("hi\n")}, nil
	}
	br := newBreaker(20)
	n := &NaabuTool{}
	ports := make([]int, 50)
	for i := range ports {
		ports[i] = i + 1
	}
	// Serialize by concurrency=1 so each recordResult fires in order — makes
	// the ratio test deterministic without racing goroutines.
	_, _, _ = n.runScan(context.Background(), []string{"1.2.3.4"}, ports, 100*time.Millisecond, 1, 1, true, br, dial)
	// After 50 attempts with timeouts > 50%, cap should have halved at least once.
	assert.GreaterOrEqual(t, br.halvingsCount(), 1, "breaker should have halved under timeout burst")
	assert.LessOrEqual(t, br.inflightCap(), 10)
}

// --- SPEC-QA2 R4: error classification ---

func TestNoSuchHostLoggedOnce(t *testing.T) {
	dial := func(network, addr string, timeout time.Duration) (net.Conn, error) {
		return nil, errors.New("dial tcp: lookup foo.invalid: no such host")
	}
	n := &NaabuTool{}
	br := newBreaker(20)
	ports := []int{22, 80, 443}
	_, errs, _ := n.runScan(context.Background(), []string{"foo.invalid"}, ports, time.Second, 20, 4, true, br, dial)
	// 3 ports × 1 host → 1 unique (ip, err-class) entry, not 3.
	assert.Len(t, errs, 1, "dial errors must be deduplicated per (ip, err-class)")
	assert.Equal(t, "no_such_host", errs[0].err)
}

func TestIsTimeoutOSErrDeadlineExceeded(t *testing.T) {
	assert.True(t, isTimeout(os.ErrDeadlineExceeded))
	assert.True(t, isTimeout(fakeTimeoutErr{}))
	assert.False(t, isTimeout(errors.New("connection refused")))
	assert.False(t, isTimeout(nil))
}

func TestClassifyErr(t *testing.T) {
	tests := []struct {
		err  string
		want string
	}{
		{"lookup foo: no such host", "no_such_host"},
		{"network is unreachable", "network_unreachable"},
		{"no route to host", "no_route"},
		{"connection refused", "refused"},
		{"garbled binary data", "other"},
	}
	for _, tc := range tests {
		got := classifyErr(errors.New(tc.err))
		assert.Equal(t, tc.want, got, "err: %q", tc.err)
	}
	assert.Equal(t, "timeout", classifyErr(fakeTimeoutErr{}))
	assert.Equal(t, "", classifyErr(nil))
}

// --- SPEC-QA2 R5/R6/R7: banner grab scenarios ---

// bannerFixture starts a one-shot TCP listener that responds with the given
// mode once a connection comes in. Returns the port the listener is on.
type bannerFixture struct {
	ln   net.Listener
	port int
}

func (b *bannerFixture) close() { b.ln.Close() } //nolint:errcheck

// newImmediateBanner: server writes a banner on connect and closes.
func newImmediateBanner(t *testing.T, banner string) *bannerFixture {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, pStr, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(pStr)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte(banner)) //nolint:errcheck
			conn.Close()               //nolint:errcheck
		}
	}()
	return &bannerFixture{ln: ln, port: p}
}

// newHangingServer: server accepts the connection but sends nothing,
// keeps the socket open for ~5s. Used to verify filtered-status semantics.
func newHangingServer(t *testing.T) *bannerFixture {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, pStr, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(pStr)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection open briefly, never send anything.
			go func(c net.Conn) {
				time.Sleep(5 * time.Second)
				c.Close() //nolint:errcheck
			}(conn)
		}
	}()
	return &bannerFixture{ln: ln, port: p}
}

func TestBannerPassiveRead(t *testing.T) {
	fx := newImmediateBanner(t, "SSH-2.0-OpenSSH_8.9p1\r\n")
	defer fx.close()

	n := NewNaabuTool()
	result, err := n.Run(context.Background(), []string{"127.0.0.1"}, RunOptions{
		ExtraArgs: map[string]string{"ports": strconv.Itoa(fx.port)},
	})
	require.NoError(t, err)
	require.Len(t, result.Assets, 1)
	md := result.Assets[0].Metadata
	assert.Equal(t, "open", md["status"])
	assert.Contains(t, md["banner_preview"].(string), "SSH-2.0")
}

func TestBannerFilteredOnHang(t *testing.T) {
	fx := newHangingServer(t)
	defer fx.close()

	// Use a port NOT in httpLikelyPorts so no active HTTP probe fires.
	// The ephemeral port the test listener grabs is in the 30000+ range,
	// which is not http-likely, so passive read (1s) times out and we
	// emit status=filtered.
	n := NewNaabuTool()
	result, err := n.Run(context.Background(), []string{"127.0.0.1"}, RunOptions{
		ExtraArgs: map[string]string{"ports": strconv.Itoa(fx.port)},
	})
	require.NoError(t, err)
	require.Len(t, result.Assets, 1)
	md := result.Assets[0].Metadata
	assert.Equal(t, "filtered", md["status"])
	assert.Equal(t, "", md["banner_preview"])
}

// TestBannerActiveHTTPProbe: an HTTP-likely port that says nothing until
// it receives a request gets a banner via the active GET path.
func TestBannerActiveHTTPProbe(t *testing.T) {
	// We need a port that is in httpLikelyPorts, so bind specifically to
	// 8080 if we can. If it's already taken, skip — not every CI env has
	// 8080 free.
	ln, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		t.Skip("port 8080 in use, skipping active-probe test")
	}
	_, pStr, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(pStr)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				// Give the client more time than its 1s passive-read
				// deadline, otherwise our Read times out at the same
				// moment the client's passive read does and we never
				// see the request.
				buf := make([]byte, 512)
				c.SetReadDeadline(time.Now().Add(4 * time.Second)) //nolint:errcheck
				rn, _ := c.Read(buf)
				if rn > 0 {
					c.Write([]byte("HTTP/1.0 200 OK\r\n\r\n")) //nolint:errcheck
				}
				c.Close() //nolint:errcheck
			}(conn)
		}
	}()
	defer ln.Close() //nolint:errcheck

	n := NewNaabuTool()
	result, err := n.Run(context.Background(), []string{"127.0.0.1"}, RunOptions{
		ExtraArgs: map[string]string{"ports": strconv.Itoa(p)},
	})
	require.NoError(t, err)
	require.Len(t, result.Assets, 1)
	md := result.Assets[0].Metadata
	assert.Equal(t, "open", md["status"])
	assert.Contains(t, md["banner_preview"].(string), "HTTP/1.")
}

// --- SPEC-QA2 R8: banner sanitizer ---

func TestSanitizeBannerTruncatesTo64(t *testing.T) {
	raw := make([]byte, 200)
	for i := range raw {
		raw[i] = 'A'
	}
	got := sanitizeBanner(raw)
	assert.Len(t, got, 64)
}

func TestSanitizeBannerReplacesControlBytesWithDot(t *testing.T) {
	raw := []byte{'H', 'i', 0x00, 0xFF, 'a'}
	got := sanitizeBanner(raw)
	assert.Equal(t, "Hi..a", got)
}

func TestSanitizeBannerPreservesWhitespace(t *testing.T) {
	raw := []byte("HTTP/1.1 200 OK\r\nServer: nginx\r\n\r\n")
	got := sanitizeBanner(raw)
	assert.Contains(t, got, "\r\n")
	assert.Contains(t, got, "HTTP/1.1")
}

func TestSanitizeBannerOneByteInOneByteOut(t *testing.T) {
	raw := []byte{0x01, 0x02, 0x03}
	got := sanitizeBanner(raw)
	assert.Len(t, got, 3, "no expansion on sanitize")
	assert.Equal(t, "...", got)
}

// --- SPEC-QA2 R9: metadata + halvings counter ---

func TestToolRunExposesHalvingsCounter(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close() //nolint:errcheck
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close() //nolint:errcheck
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())

	n := NewNaabuTool()
	result, err := n.Run(context.Background(), []string{"127.0.0.1"}, RunOptions{
		ExtraArgs: map[string]string{"ports": portStr},
	})
	require.NoError(t, err)
	// On a cooperative localhost scan we expect no halvings.
	halvings, ok := result.ToolRun.Config["concurrency_halvings"]
	require.True(t, ok, "concurrency_halvings must be exposed on ToolRun.Config")
	assert.Equal(t, 0, halvings)

	defaultC, ok := result.ToolRun.Config["default_concurrency"]
	require.True(t, ok)
	assert.Equal(t, 20, defaultC)
}

// --- SPEC-QA2 R11: --no-banner flag suppresses bytes on the wire ---

func TestNoBannerModeSkipsPayloadRead(t *testing.T) {
	// Server that would immediately send a banner; with --no-banner we
	// should still report open, but banner_preview must be empty.
	fx := newImmediateBanner(t, "SSH-2.0-x\r\n")
	defer fx.close()

	n := NewNaabuTool()
	result, err := n.Run(context.Background(), []string{"127.0.0.1"}, RunOptions{
		ExtraArgs: map[string]string{
			"ports":     strconv.Itoa(fx.port),
			"no-banner": "true",
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Assets, 1)
	md := result.Assets[0].Metadata
	assert.Equal(t, "open", md["status"])
	assert.Equal(t, "", md["banner_preview"])
}
