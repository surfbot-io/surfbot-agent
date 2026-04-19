package detection

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

var titleRegex = regexp.MustCompile(`(?i)<title[^>]*>([^<]+)</title>`)

// TechRule defines a technology detection pattern.
type TechRule struct {
	Name         string
	Headers      map[string]string
	BodyContains []string
}

var techRules = []TechRule{
	{Name: "WordPress", Headers: map[string]string{"x-powered-by": "WordPress"}, BodyContains: []string{"/wp-content/", "/wp-includes/"}},
	{Name: "Drupal", BodyContains: []string{"Drupal.settings", "/sites/default/files"}},
	{Name: "Joomla", BodyContains: []string{"/media/jui/", "Joomla!"}},
	{Name: "PHP", Headers: map[string]string{"x-powered-by": "PHP"}},
	{Name: "ASP.NET", Headers: map[string]string{"x-powered-by": "ASP.NET"}},
	{Name: "nginx", Headers: map[string]string{"server": "nginx"}},
	{Name: "Apache", Headers: map[string]string{"server": "Apache"}},
	{Name: "IIS", Headers: map[string]string{"server": "Microsoft-IIS"}},
	{Name: "Cloudflare", Headers: map[string]string{"server": "cloudflare"}},
	{Name: "Express", Headers: map[string]string{"x-powered-by": "Express"}},
	{Name: "React", BodyContains: []string{"__NEXT_DATA__", "react-root", "_react"}},
	{Name: "Vue.js", BodyContains: []string{"__vue__", "vue-app"}},
}

// HTTPXTool probes HTTP services using the Go standard library.
type HTTPXTool struct{}

func NewHTTPXTool() *HTTPXTool { return &HTTPXTool{} }

// httpxTransportOverride lets tests substitute the http.RoundTripper used
// by HTTPXTool without rewiring net.Listen. Production callers leave it
// nil; the default *http.Transport is constructed in Run.
var httpxTransportOverride http.RoundTripper

// resolveHttpxParams returns the params HTTPXTool should run with,
// preferring opts.HttpxParams when supplied and falling back to
// model.DefaultHttpxParams() per-field. The legacy opts.RateLimit
// overrides Threads when typed params are absent so existing callers
// keep working unchanged.
func resolveHttpxParams(opts RunOptions) model.HttpxParams {
	defaults := model.DefaultHttpxParams()
	if opts.HttpxParams == nil {
		if opts.RateLimit > 0 {
			defaults.Threads = opts.RateLimit
		}
		return defaults
	}
	resolved := *opts.HttpxParams
	if resolved.Threads <= 0 {
		resolved.Threads = defaults.Threads
	}
	if len(resolved.Probes) == 0 {
		resolved.Probes = defaults.Probes
	}
	if resolved.Timeout <= 0 {
		resolved.Timeout = defaults.Timeout
	}
	return resolved
}

func (h *HTTPXTool) Name() string    { return "httpx" }
func (h *HTTPXTool) Phase() string   { return "http_probe" }
func (h *HTTPXTool) Kind() ToolKind  { return ToolKindNative }
func (h *HTTPXTool) Available() bool { return true }

func (h *HTTPXTool) Command() string { return "probe" }
func (h *HTTPXTool) Description() string {
	return "Probe host:port pairs for live HTTP services and detect technologies"
}
func (h *HTTPXTool) InputType() string     { return "hostports" }
func (h *HTTPXTool) OutputTypes() []string { return []string{"url", "technology"} }

// probeTarget describes a single HTTP probe attempt.
//
// Input formats accepted by buildProbeURLs (see SUR-242):
//   - "hostname|ip:port/tcp" — probe IP but send the hostname in the Host header.
//     The response is dropped if the effective host does not match ExpectedHost
//     (vhost scope check).
//   - "ip:port/tcp" — IP-pure probe: no Host override, no scope check. Whatever
//     the server returns is attributed to the IP.
//   - "hostname" (no port) — bare hostname probe via DNS; ExpectedHost = hostname
//     so redirects to out-of-scope hosts are still dropped.
type probeTarget struct {
	URL          string // URL to fetch (protocol://host:port)
	ExpectedHost string // hostname we expect to reach; empty = IP-pure (no scope check)
	IP           string // for structured mismatch log
	Port         int    // for structured mismatch log
}

type probeResult struct {
	URL        string
	StatusCode int
	Title      string
	Server     string
	TechList   []string
	Metadata   map[string]interface{}
}

func (h *HTTPXTool) Run(ctx context.Context, inputs []string, opts RunOptions) (*RunResult, error) {
	startedAt := time.Now().UTC()

	params := resolveHttpxParams(opts)
	concurrency := params.Threads

	transport := http.RoundTripper(&http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // intentional: scanning unknown targets
		},
		MaxIdleConnsPerHost: 10,
	})
	if httpxTransportOverride != nil {
		transport = httpxTransportOverride
	}
	checkRedirect := func(req *http.Request, via []*http.Request) error {
		if !params.FollowRedirects {
			return http.ErrUseLastResponse
		}
		if len(via) >= 3 {
			return http.ErrUseLastResponse
		}
		return nil
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       params.Timeout,
		CheckRedirect: checkRedirect,
	}

	targets := filterProbesBySchemes(buildProbeURLs(inputs), params.Probes)

	var results []probeResult
	var drops int
	var mu sync.Mutex

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(target probeTarget) {
			defer wg.Done()
			defer func() { <-sem }()

			pr, dropped := probeURL(ctx, client, target)
			mu.Lock()
			defer mu.Unlock()
			if dropped {
				drops++
				return
			}
			if pr != nil {
				results = append(results, *pr)
			}
		}(t)
	}

	wg.Wait()

	runResult := &RunResult{}
	for _, pr := range results {
		urlAssetID := uuid.New().String()
		runResult.Assets = append(runResult.Assets, model.Asset{
			ID:        urlAssetID,
			Type:      model.AssetTypeURL,
			Value:     pr.URL,
			Status:    model.AssetStatusNew,
			Tags:      []string{},
			Metadata:  pr.Metadata,
			FirstSeen: time.Now().UTC(),
			LastSeen:  time.Now().UTC(),
		})

		for _, tech := range pr.TechList {
			runResult.Assets = append(runResult.Assets, model.Asset{
				ID:        uuid.New().String(),
				Type:      model.AssetTypeTechnology,
				Value:     tech,
				ParentID:  urlAssetID,
				Status:    model.AssetStatusNew,
				Tags:      []string{},
				Metadata:  map[string]interface{}{},
				FirstSeen: time.Now().UTC(),
				LastSeen:  time.Now().UTC(),
			})
		}
	}

	urlCount, techCount := 0, 0
	for _, a := range runResult.Assets {
		switch a.Type {
		case model.AssetTypeURL:
			urlCount++
		case model.AssetTypeTechnology:
			techCount++
		}
	}

	tr := buildToolRun(h, startedAt, model.ToolRunCompleted, "", len(inputs), len(runResult.Assets))
	tr.OutputSummary = fmt.Sprintf("Probed %d target(s) → %d live URL(s), %d technolog(ies), %d dropped (vhost mismatch)",
		len(targets), urlCount, techCount, drops)
	attachExecContext(&tr,
		fmt.Sprintf("httpx (in-process HTTP prober, concurrency=%d, timeout=%s)", concurrency, params.Timeout),
		0,
		"", // httpx's per-probe dropped/failure reasons already go to os.Stderr via the vhostMismatchLog writer and would be too verbose here
		inputs,
	)
	if drops > 0 {
		tr.Config["vhost_mismatch_drops"] = drops
	}
	tr.Config["targets_probed"] = len(targets)
	tr.Config["url_count"] = urlCount
	tr.Config["tech_count"] = techCount
	runResult.ToolRun = tr
	return runResult, nil
}

// filterProbesBySchemes drops probes whose scheme is not in the requested
// set. An empty or nil schemes list returns the targets unchanged.
func filterProbesBySchemes(targets []probeTarget, schemes []string) []probeTarget {
	if len(schemes) == 0 {
		return targets
	}
	allow := map[string]bool{}
	for _, s := range schemes {
		allow[strings.ToLower(s)] = true
	}
	if allow["http"] && allow["https"] {
		return targets
	}
	out := make([]probeTarget, 0, len(targets))
	for _, t := range targets {
		scheme := "http"
		if strings.HasPrefix(t.URL, "https://") {
			scheme = "https"
		}
		if allow[scheme] {
			out = append(out, t)
		}
	}
	return out
}

// buildProbeURLs parses a list of probe inputs into concrete probe targets.
// Accepted forms: "hostname|ip:port/tcp", "ip:port/tcp", "hostname". See
// probeTarget docs for semantics.
func buildProbeURLs(inputs []string) []probeTarget {
	httpsDefaultPorts := map[int]bool{443: true, 8443: true, 9443: true}
	var targets []probeTarget

	for _, raw := range inputs {
		// Optional "hostname|" prefix (SUR-242 enriched format).
		expectedHost := ""
		body := raw
		if idx := strings.Index(raw, "|"); idx >= 0 {
			expectedHost = raw[:idx]
			body = raw[idx+1:]
		}

		body = strings.TrimSuffix(body, "/tcp")

		host, portStr, err := splitHostPort(body)
		if err != nil {
			// Bare hostname/IP: probe via DNS. Treat a raw IP as IP-pure
			// (no scope check); treat a hostname as self-scoped so off-site
			// redirects still trip the check.
			h := body
			if expectedHost == "" && net.ParseIP(h) == nil {
				expectedHost = h
			}
			targets = append(targets,
				probeTarget{URL: "http://" + h, ExpectedHost: expectedHost},
				probeTarget{URL: "https://" + h, ExpectedHost: expectedHost},
			)
			continue
		}

		port := 0
		fmt.Sscanf(portStr, "%d", &port)

		// If the pre-`|` section was empty and the host is a DNS name rather
		// than an IP, self-scope: naabu emits "hostname:port/tcp" assets when
		// fed hostnames directly, and those arrive here unenriched. Without
		// self-scoping them the probe would be treated as IP-pure and a
		// redirect to an out-of-scope vhost would be silently persisted.
		if expectedHost == "" && net.ParseIP(host) == nil {
			expectedHost = host
		}

		// IP field is only meaningful when host is actually an IP — the log
		// line and the plaintext-HTTP scope lenience both key off it.
		dialedIP := ""
		if net.ParseIP(host) != nil {
			dialedIP = host
		}

		var schemes []string
		switch {
		case httpsDefaultPorts[port]:
			schemes = []string{"https"}
		case port == 80:
			schemes = []string{"http"}
		default:
			schemes = []string{"http", "https"}
		}
		for _, scheme := range schemes {
			targets = append(targets, probeTarget{
				URL:          fmt.Sprintf("%s://%s:%d", scheme, host, port),
				ExpectedHost: expectedHost,
				IP:           dialedIP,
				Port:         port,
			})
		}
	}

	return targets
}

func splitHostPort(input string) (string, string, error) {
	// Handle IPv6 addresses in brackets
	if strings.HasPrefix(input, "[") {
		closeIdx := strings.LastIndex(input, "]")
		if closeIdx < 0 {
			return "", "", fmt.Errorf("unmatched [ in %q", input)
		}
		rest := input[closeIdx+1:]
		if !strings.HasPrefix(rest, ":") {
			return "", "", fmt.Errorf("no port in %q", input)
		}
		return input[1:closeIdx], rest[1:], nil
	}
	idx := strings.LastIndex(input, ":")
	if idx < 0 {
		return "", "", fmt.Errorf("no port in %q", input)
	}
	return input[:idx], input[idx+1:], nil
}

// probeURL issues a single HTTP probe. Returns (result, droppedAsMismatch).
// When target.ExpectedHost is non-empty, the request is issued with Host set
// to ExpectedHost, and the response is dropped (returning nil, true) when the
// effective host (final URL hostname, and TLS cert SAN coverage for HTTPS)
// does not match. See SUR-242.
func probeURL(ctx context.Context, client *http.Client, target probeTarget) (*probeResult, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("User-Agent", "surfbot-agent/1.0")
	if target.ExpectedHost != "" {
		req.Host = target.ExpectedHost
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()

	// Scope check: if we asked for a specific vhost, verify the response came
	// from something that matches. Dropped responses never become assets.
	if target.ExpectedHost != "" {
		observed := resp.Request.URL.Hostname()
		if !scopeMatches(target.ExpectedHost, observed, resp.TLS, target.IP) {
			logVhostMismatch(target, observed, resp.StatusCode)
			return nil, true
		}
	}

	// Read up to 64KB
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	bodyStr := string(body)

	pr := &probeResult{
		URL:        recordedURL(resp.Request.URL, target),
		StatusCode: resp.StatusCode,
		Server:     resp.Header.Get("Server"),
		Metadata:   map[string]interface{}{},
	}

	pr.Title = ExtractTitle(bodyStr)

	pr.Metadata["status_code"] = resp.StatusCode
	if pr.Title != "" {
		pr.Metadata["title"] = pr.Title
	}
	if pr.Server != "" {
		pr.Metadata["server"] = pr.Server
	}
	if resp.ContentLength >= 0 {
		pr.Metadata["content_length"] = resp.ContentLength
	}

	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		cert := resp.TLS.PeerCertificates[0]
		pr.Metadata["tls_issuer"] = cert.Issuer.CommonName
		pr.Metadata["tls_expiry"] = cert.NotAfter.Format(time.RFC3339)
		pr.Metadata["tls_valid"] = time.Now().Before(cert.NotAfter) && time.Now().After(cert.NotBefore)
	}

	pr.TechList = DetectTechnologies(resp.Header, bodyStr)

	return pr, false
}

// scopeMatches reports whether the observed hostname belongs to the expected
// scope. Match rules, in order:
//  1. Case-insensitive equality of the URL hostname against expected.
//  2. TLS cert CN/SANs cover expected (wildcard SAN support via stdlib).
//  3. Plaintext HTTP dialed by IP with no redirect: the final URL still
//     points at the IP we originally dialed, so no off-site hop happened.
//     Without TLS there's no cryptographic proof the server honored the
//     Host header, but a redirect-to-default-vhost would already have
//     tripped rule 1; a silent default-vhost is indistinguishable from a
//     correct response at the HTTP layer, so we trust it.
func scopeMatches(expected, observed string, cs *tls.ConnectionState, dialedIP string) bool {
	if strings.EqualFold(expected, observed) {
		return true
	}
	if cs != nil && len(cs.PeerCertificates) > 0 {
		if certCoversHost(cs.PeerCertificates[0], expected) {
			return true
		}
	}
	if cs == nil && dialedIP != "" && strings.EqualFold(observed, dialedIP) {
		return true
	}
	return false
}

// certCoversHost returns true when the cert's CN/SANs cover hostname, including
// RFC 6125 wildcard matching. Delegates to the stdlib.
func certCoversHost(cert *x509.Certificate, hostname string) bool {
	return cert.VerifyHostname(hostname) == nil
}

// recordedURL returns the URL to persist as an asset. For scoped probes we
// rewrite the IP-based URL back to the hostname so downstream assets are
// attributed to the user-declared target, not the raw IP.
func recordedURL(final *url.URL, target probeTarget) string {
	if target.ExpectedHost == "" {
		return final.String()
	}
	rewritten := *final
	if target.Port > 0 && !isDefaultPort(rewritten.Scheme, target.Port) {
		rewritten.Host = fmt.Sprintf("%s:%d", target.ExpectedHost, target.Port)
	} else {
		rewritten.Host = target.ExpectedHost
	}
	return rewritten.String()
}

func isDefaultPort(scheme string, port int) bool {
	return (scheme == "http" && port == 80) || (scheme == "https" && port == 443)
}

// vhostMismatchLog is the writer for structured drop logs. Swappable in tests.
var vhostMismatchLog io.Writer = os.Stderr

func logVhostMismatch(target probeTarget, observed string, status int) {
	fmt.Fprintf(vhostMismatchLog,
		"reason=vhost_mismatch expected_host=%s observed_host=%s ip=%s port=%d status=%d\n",
		target.ExpectedHost, observed, target.IP, target.Port, status)
}

// ExtractTitle extracts the HTML title from a response body string.
func ExtractTitle(body string) string {
	matches := titleRegex.FindStringSubmatch(body)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// DetectTechnologies matches response headers and body against tech rules.
func DetectTechnologies(headers http.Header, body string) []string {
	var detected []string
	bodyLower := strings.ToLower(body)

	for _, rule := range techRules {
		hasHeaders := len(rule.Headers) > 0
		hasBody := len(rule.BodyContains) > 0

		headerMatch := true
		if hasHeaders {
			headerMatch = matchHeaders(headers, rule.Headers)
		}

		bodyMatch := false
		if hasBody {
			bodyMatch = matchBody(bodyLower, rule.BodyContains)
		}

		if hasHeaders && hasBody {
			// Both must match
			if headerMatch && bodyMatch {
				detected = append(detected, rule.Name)
			}
		} else if hasHeaders {
			if headerMatch {
				detected = append(detected, rule.Name)
			}
		} else if hasBody {
			if bodyMatch {
				detected = append(detected, rule.Name)
			}
		}
	}

	return detected
}

func matchHeaders(headers http.Header, rules map[string]string) bool {
	for key, pattern := range rules {
		val := headers.Get(key)
		if val == "" {
			return false
		}
		if !strings.Contains(strings.ToLower(val), strings.ToLower(pattern)) {
			return false
		}
	}
	return true
}

func matchBody(bodyLower string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(bodyLower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}
