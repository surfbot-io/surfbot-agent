package detection

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// Top100Ports is the default set of ports to scan.
var Top100Ports = []int{
	21, 22, 23, 25, 53, 80, 81, 110, 111, 113, 135, 139, 143, 179, 199,
	443, 445, 465, 514, 515, 548, 554, 587, 646, 993, 995, 1025, 1026,
	1027, 1433, 1720, 1723, 2000, 2001, 3000, 3001, 3128, 3306, 3389,
	4443, 4567, 5000, 5060, 5222, 5432, 5555, 5601, 5900, 5984, 5985,
	6000, 6001, 6379, 6443, 7001, 7002, 7443, 8000, 8008, 8009, 8010,
	8020, 8080, 8081, 8082, 8083, 8084, 8085, 8086, 8087, 8088, 8089,
	8090, 8091, 8181, 8443, 8880, 8888, 9000, 9001, 9090, 9091, 9200,
	9300, 9443, 9999, 10000, 10443, 27017, 27018, 28017, 50000, 50070,
}

// defaultConcurrency is the port-scan fan-out when --rate-limit is not set.
// 20 is safe on residential links and behind cloud WAFs. Users on fast
// corporate links can raise it via --rate-limit.
const defaultConcurrency = 20

// httpLikelyPorts is the set where a blind GET is worth sending when the
// passive banner read comes back empty. Strictly HTTP-ish ports; TLS-only
// ports like 443 are included because an HTTP probe still elicits some
// observable response ("Bad Request", "400", connection close), which is
// itself useful banner data. See SPEC-QA2 R6.
var httpLikelyPorts = map[int]struct{}{
	80:    {},
	443:   {},
	8000:  {},
	8008:  {},
	8080:  {},
	8081:  {},
	8082:  {},
	8083:  {},
	8088:  {},
	8443:  {},
	8888:  {},
	9443:  {},
	10443: {},
}

// NaabuTool performs TCP connect scanning using the Go standard library.
type NaabuTool struct{}

func NewNaabuTool() *NaabuTool { return &NaabuTool{} }

func (n *NaabuTool) Name() string    { return "naabu" }
func (n *NaabuTool) Phase() string   { return "port_scan" }
func (n *NaabuTool) Kind() ToolKind  { return ToolKindNative }
func (n *NaabuTool) Available() bool { return true }

func (n *NaabuTool) Command() string       { return "portscan" }
func (n *NaabuTool) Description() string   { return "Scan hosts for open TCP ports" }
func (n *NaabuTool) InputType() string     { return "ips" }
func (n *NaabuTool) OutputTypes() []string { return []string{"hostport"} }

// portProbeResult carries the outcome of one completed port probe.
type portProbeResult struct {
	IP            string
	Port          int
	Status        string // "open" or "filtered"
	BannerPreview string
}

// dialFunc abstracts net.DialTimeout so tests can inject a fake dialer.
type dialFunc func(network, addr string, timeout time.Duration) (net.Conn, error)

// defaultDialer is the production dialer.
var defaultDialer dialFunc = net.DialTimeout

func (n *NaabuTool) Run(ctx context.Context, inputs []string, opts RunOptions) (*RunResult, error) {
	startedAt := time.Now().UTC()

	ports, err := ParsePorts(opts.ExtraArgs["ports"])
	if err != nil {
		return nil, fmt.Errorf("naabu: parsing ports: %w", err)
	}

	dialTimeout := 3 * time.Second
	if t, ok := opts.ExtraArgs["timeout"]; ok {
		if secs, err := strconv.Atoi(t); err == nil && secs > 0 {
			dialTimeout = time.Duration(secs) * time.Second
		}
	}

	concurrency := opts.RateLimit
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}

	// --no-banner disables banner grab entirely (R11). Defaults to banner
	// grab enabled. Accepts "true"/"1" for the flag.
	noBanner := false
	if v, ok := opts.ExtraArgs["no-banner"]; ok {
		if v == "true" || v == "1" {
			noBanner = true
		}
	}

	br := newBreaker(concurrency)
	retryCap := concurrency / 4
	if retryCap < 2 {
		retryCap = 2
	}

	results, dialErrs, failuresAll := n.runScan(ctx, inputs, ports, dialTimeout, concurrency, retryCap, noBanner, br, defaultDialer)

	// Build a stderr-ish log from the de-duplicated dial errors so the
	// tool_run surfaces unusual failures (DNS, unroutable net, etc.)
	// without spamming. Timeouts and refused connections are omitted by
	// runScan's classifyErr filter — they're routine on a port scan.
	// Mirror each line to stderr live so the operator also sees them.
	var errLog strings.Builder
	for _, d := range dialErrs {
		line := fmt.Sprintf("[naabu] reason=dial_error ip=%s port=%d err=%s\n", d.ip, d.port, d.err)
		errLog.WriteString(line)
		fmt.Fprint(os.Stderr, line)
	}
	_ = failuresAll // reserved for future per-IP breaker, SPEC-QA2 R12

	runResult := &RunResult{}
	openCount, filteredCount := 0, 0
	for _, r := range results {
		md := map[string]interface{}{
			"port":           r.Port,
			"protocol":       "tcp",
			"ip":             r.IP,
			"status":         r.Status,
			"banner_preview": r.BannerPreview,
		}
		if r.Status == "filtered" {
			filteredCount++
		} else {
			openCount++
		}
		runResult.Assets = append(runResult.Assets, model.Asset{
			ID:        uuid.New().String(),
			Type:      model.AssetTypePort,
			Value:     fmt.Sprintf("%s:%d/tcp", r.IP, r.Port),
			Status:    model.AssetStatusNew,
			Tags:      []string{},
			Metadata:  md,
			FirstSeen: time.Now().UTC(),
			LastSeen:  time.Now().UTC(),
		})
	}

	tr := buildToolRun(n, startedAt, model.ToolRunCompleted, "", len(inputs), len(runResult.Assets))
	tr.OutputSummary = fmt.Sprintf("Probed %d host(s) × %d port(s) → %d open, %d filtered (dialTimeout=%s, concurrency=%d)",
		len(inputs), len(ports), openCount, filteredCount, dialTimeout, concurrency)
	attachExecContext(&tr,
		fmt.Sprintf("naabu (in-process scanner, ports=%s, timeout=%s, concurrency=%d, banner=%v)",
			opts.ExtraArgs["ports"], dialTimeout, concurrency, !noBanner),
		0,
		errLog.String(),
		inputs,
	)
	tr.Config["concurrency_halvings"] = br.halvingsCount()
	tr.Config["default_concurrency"] = defaultConcurrency
	tr.Config["effective_concurrency"] = concurrency
	tr.Config["ports_scanned"] = len(ports)
	tr.Config["open_count"] = openCount
	tr.Config["filtered_count"] = filteredCount
	runResult.ToolRun = tr
	return runResult, nil
}

// dialErrorClass is a surfaced non-timeout / non-refused dial error.
type dialErrorClass struct {
	ip   string
	port int
	err  string
}

// runScan is the core of the port scanner. It orchestrates the primary
// goroutine pool (sized by concurrency, adjusted by the breaker), the
// retry pool (sized to concurrency/4, min 2), and the banner-grab logic.
// Broken out of Run() to keep Run() short per CLAUDE.md.
func (n *NaabuTool) runScan(
	ctx context.Context,
	inputs []string,
	ports []int,
	dialTimeout time.Duration,
	concurrency int,
	retryCap int,
	noBanner bool,
	br *breaker,
	dial dialFunc,
) ([]portProbeResult, []dialErrorClass, int) {
	var (
		results  []portProbeResult
		dialErrs []dialErrorClass
		errSeen  = map[string]bool{}
		failures int
		mu       sync.Mutex
	)

	sem := make(chan struct{}, concurrency)
	retrySem := make(chan struct{}, retryCap)
	var wg sync.WaitGroup

	recordDialErr := func(ip string, port int, err error) {
		key := ip + "|" + classifyErr(err)
		mu.Lock()
		defer mu.Unlock()
		if errSeen[key] {
			return
		}
		errSeen[key] = true
		dialErrs = append(dialErrs, dialErrorClass{ip: ip, port: port, err: classifyErr(err)})
	}

	recordFailure := func() {
		mu.Lock()
		defer mu.Unlock()
		failures++
	}

	appendResult := func(r portProbeResult) {
		mu.Lock()
		defer mu.Unlock()
		results = append(results, r)
	}

	// breakerLog emits at most one log line per halving/restoration event.
	breakerLog := func(event string, oldCap, newCap int, ratio float64) {
		switch event {
		case "halved":
			fmt.Fprintf(os.Stderr, "[naabu] reason=concurrency_halved old=%d new=%d timeout_ratio=%.2f\n", oldCap, newCap, ratio)
		case "restored":
			fmt.Fprintf(os.Stderr, "[naabu] reason=concurrency_restored new=%d\n", newCap)
		}
	}

	// inflightCheck blocks the caller until the breaker's effective cap
	// would admit one more connection. Implemented as a sloppy atomic so
	// it's wait-free in the common case.
	var inflight int64
	tryAdmit := func() bool {
		cap := int64(br.inflightCap())
		if atomic.AddInt64(&inflight, 1) <= cap {
			return true
		}
		atomic.AddInt64(&inflight, -1)
		return false
	}
	release := func() { atomic.AddInt64(&inflight, -1) }

	for _, ip := range inputs {
		for _, port := range ports {
			if ctx.Err() != nil {
				break
			}
			wg.Add(1)
			// Acquire semaphore before spawning; honors the user's --rate-limit
			// as a hard ceiling. The breaker lowers the effective parallelism
			// below that ceiling via tryAdmit().
			sem <- struct{}{}
			go func(ip string, port int) {
				defer wg.Done()
				defer func() { <-sem }()

				// Wait for the breaker's current cap.
				for !tryAdmit() {
					// Brief back-off so we don't busy-spin if the breaker is
					// clamping hard. The sleep is short (10ms) so we catch
					// capacity the moment it opens back up.
					select {
					case <-ctx.Done():
						return
					case <-time.After(10 * time.Millisecond):
					}
				}
				defer release()

				res, success, timedOut, dialErr := probePort(ctx, ip, port, dialTimeout, noBanner, dial)

				// Retry once on timeout via the separate retry pool. Timeout
				// retries do not re-consume the main semaphore — they're
				// rate-limited by retryCap so a retry storm can't amplify the
				// very condition that caused the timeout.
				if !success && timedOut {
					retrySem <- struct{}{}
					res, success, timedOut, dialErr = retryProbe(ctx, ip, port, dialTimeout, noBanner, dial)
					<-retrySem
				}

				// Record into breaker AFTER any retry completes.
				event, oldCap, newCap, ratio := br.recordResult(success, timedOut)
				breakerLog(event, oldCap, newCap, ratio)

				if success {
					appendResult(res)
					return
				}

				if timedOut {
					recordFailure()
					return
				}

				// Not a timeout and not a successful connect: a RST/refused
				// is a definitive "closed port" and counts neither as a
				// failure in the breaker nor as a dial error to surface.
				if isRefused(dialErr) {
					return
				}

				// Anything else (DNS, unroutable, etc.) gets logged once.
				if dialErr != nil {
					recordDialErr(ip, port, dialErr)
				}
			}(ip, port)
		}
	}

	wg.Wait()
	return results, dialErrs, failures
}

// probePort performs one dial + banner-grab attempt. Returns the probe
// result (valid only when success==true), the success/timedOut flags used
// by the breaker, and the underlying dial error for classification.
func probePort(ctx context.Context, ip string, port int, dialTimeout time.Duration, noBanner bool, dial dialFunc) (portProbeResult, bool, bool, error) {
	if ctx.Err() != nil {
		return portProbeResult{}, false, false, ctx.Err()
	}
	addr := fmt.Sprintf("%s:%d", ip, port)
	conn, err := dial("tcp", addr, dialTimeout)
	if err != nil {
		return portProbeResult{}, false, isTimeout(err), err
	}
	defer conn.Close() //nolint:errcheck

	pr := portProbeResult{IP: ip, Port: port, Status: "open"}

	if noBanner {
		// R11: no bytes on the wire beyond the handshake.
		pr.BannerPreview = ""
		return pr, true, false, nil
	}

	banner := grabBanner(conn, port)
	if len(banner) == 0 {
		pr.Status = "filtered"
		pr.BannerPreview = ""
		return pr, true, false, nil
	}
	pr.BannerPreview = sanitizeBanner(banner)
	return pr, true, false, nil
}

// retryProbe wraps probePort with a 200–800ms jitter. Separate function so
// the jitter is in one place and easy to tune.
func retryProbe(ctx context.Context, ip string, port int, dialTimeout time.Duration, noBanner bool, dial dialFunc) (portProbeResult, bool, bool, error) {
	jitter := 200 + rand.Intn(601) // 200..800 inclusive
	select {
	case <-ctx.Done():
		return portProbeResult{}, false, false, ctx.Err()
	case <-time.After(time.Duration(jitter) * time.Millisecond):
	}
	return probePort(ctx, ip, port, dialTimeout, noBanner, dial)
}

// grabBanner implements SPEC-QA2 R5+R6: passive read with a 1s deadline
// first, then an active HTTP GET on http-likely ports if the passive read
// comes back empty. Returns up to 256 raw bytes; callers are responsible
// for sanitizing and truncating for storage.
func grabBanner(conn net.Conn, port int) []byte {
	buf := make([]byte, 256)
	_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, _ := conn.Read(buf)
	if n > 0 {
		return buf[:n]
	}

	// Passive read returned nothing — for HTTP-likely ports, try sending a
	// blind GET and see what the server says. Plaintext probe even on 443:
	// the goal is to confirm "something is listening", not to speak TLS
	// (that's httpx's job). A TLS server typically closes or errors out,
	// which is still a useful fingerprint.
	if _, isHTTP := httpLikelyPorts[port]; !isHTTP {
		return nil
	}
	_ = conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := conn.Write([]byte("GET / HTTP/1.0\r\n\r\n")); err != nil {
		return nil
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ = conn.Read(buf)
	if n > 0 {
		return buf[:n]
	}
	return nil
}

// sanitizeBanner implements SPEC-QA2 R8: cap at 64 bytes; replace control /
// non-ASCII bytes with '.' (keeping \r \n \t for readability).
// One byte in, one byte out — no expansion.
func sanitizeBanner(raw []byte) string {
	const maxLen = 64
	if len(raw) > maxLen {
		raw = raw[:maxLen]
	}
	out := make([]byte, len(raw))
	for i, b := range raw {
		switch {
		case b == '\r' || b == '\n' || b == '\t':
			out[i] = b
		case b < 0x20 || b > 0x7E:
			out[i] = '.'
		default:
			out[i] = b
		}
	}
	return string(out)
}

// isTimeout tells us whether a dial error represents a network-level stall
// (the target didn't answer within the timeout) as opposed to a definitive
// closed-port signal (RST / ECONNREFUSED). Uses both os.ErrDeadlineExceeded
// and the net.Error Timeout() method so we match behavior across Linux,
// macOS, and Windows.
func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var nerr net.Error
	if errors.As(err, &nerr) {
		return nerr.Timeout()
	}
	return false
}

// isRefused returns true for a clean "closed port" signal — RST or
// ECONNREFUSED — which is not a breaker failure.
func isRefused(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "connection refused") || strings.Contains(s, "refused")
}

// classifyErr produces a short label for log deduplication. We don't want a
// million "no such host: foo.example.com" lines in the output — one per
// unique (ip, class) is enough.
func classifyErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "no such host"):
		return "no_such_host"
	case strings.Contains(s, "network is unreachable"):
		return "network_unreachable"
	case strings.Contains(s, "no route to host"):
		return "no_route"
	case isTimeout(err):
		return "timeout"
	case isRefused(err):
		return "refused"
	default:
		return "other"
	}
}

// ParsePorts parses a port specification string into a list of ports.
// Supported formats: "80,443", "1-100", "top100", "" (default top100).
func ParsePorts(spec string) ([]int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "top100" {
		return Top100Ports, nil
	}

	if spec == "top1000" {
		// For now, top1000 just returns top100 — full list would be added later
		return Top100Ports, nil
	}

	var ports []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			bounds := strings.SplitN(part, "-", 2)
			start, err := strconv.Atoi(strings.TrimSpace(bounds[0]))
			if err != nil {
				return nil, fmt.Errorf("invalid port range start: %q", bounds[0])
			}
			end, err := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid port range end: %q", bounds[1])
			}
			if start > end || start < 1 || end > 65535 {
				return nil, fmt.Errorf("invalid port range: %d-%d", start, end)
			}
			for p := start; p <= end; p++ {
				ports = append(ports, p)
			}
		} else {
			p, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid port: %q", part)
			}
			if p < 1 || p > 65535 {
				return nil, fmt.Errorf("port out of range: %d", p)
			}
			ports = append(ports, p)
		}
	}

	return ports, nil
}
