package detection

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
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

func (h *HTTPXTool) Name() string   { return "httpx" }
func (h *HTTPXTool) Phase() string  { return "http_probe" }
func (h *HTTPXTool) Kind() ToolKind { return ToolKindNative }
func (h *HTTPXTool) Available() bool { return true }

func (h *HTTPXTool) Command() string       { return "probe" }
func (h *HTTPXTool) Description() string   { return "Probe host:port pairs for live HTTP services and detect technologies" }
func (h *HTTPXTool) InputType() string     { return "hostports" }

// InputTypes reflects that httpx.Run tolerates bare domains in
// addition to host:port pairs — when given a domain with no port,
// it probes :80 and :443 (standard ProjectDiscovery behavior,
// preserved by buildProbeURLs below). Declaring both lets the
// agent-spec pipe graph include a direct discover→probe shortcut
// alongside the full portscan→probe chain.
func (h *HTTPXTool) InputTypes() []string  { return []string{"hostports", "domains"} }
func (h *HTTPXTool) OutputTypes() []string { return []string{"url", "technology"} }

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

	concurrency := opts.RateLimit
	if concurrency <= 0 {
		concurrency = 50
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // intentional: scanning unknown targets
			},
			MaxIdleConnsPerHost: 10,
		},
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	// Build probe URLs from inputs (ip:port/tcp format from naabu)
	urls := buildProbeURLs(inputs)

	var results []probeResult
	var mu sync.Mutex

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, u := range urls {
		wg.Add(1)
		sem <- struct{}{}
		go func(targetURL string) {
			defer wg.Done()
			defer func() { <-sem }()

			pr := probeURL(ctx, client, targetURL)
			if pr != nil {
				mu.Lock()
				results = append(results, *pr)
				mu.Unlock()
			}
		}(u)
	}

	wg.Wait()

	runResult := &RunResult{}
	for _, pr := range results {
		urlAssetID := uuid.New().String()
		runResult.Assets = append(runResult.Assets, model.Asset{
			ID:       urlAssetID,
			Type:     model.AssetTypeURL,
			Value:    pr.URL,
			Status:   model.AssetStatusNew,
			Tags:     []string{},
			Metadata: pr.Metadata,
			FirstSeen: time.Now().UTC(),
			LastSeen:  time.Now().UTC(),
		})

		for _, tech := range pr.TechList {
			runResult.Assets = append(runResult.Assets, model.Asset{
				ID:       uuid.New().String(),
				Type:     model.AssetTypeTechnology,
				Value:    tech,
				ParentID: urlAssetID,
				Status:   model.AssetStatusNew,
				Tags:     []string{},
				Metadata: map[string]interface{}{},
				FirstSeen: time.Now().UTC(),
				LastSeen:  time.Now().UTC(),
			})
		}
	}

	runResult.ToolRun = buildToolRun(h, startedAt, model.ToolRunCompleted, "", len(inputs), len(runResult.Assets))
	return runResult, nil
}

func buildProbeURLs(inputs []string) []string {
	httpsDefaultPorts := map[int]bool{443: true, 8443: true, 9443: true}
	var urls []string

	for _, input := range inputs {
		// Input format from naabu: "ip:port/tcp"
		input = strings.TrimSuffix(input, "/tcp")
		host, portStr, err := splitHostPort(input)
		if err != nil {
			// Try as plain host
			urls = append(urls, "http://"+input, "https://"+input)
			continue
		}

		port := 0
		fmt.Sscanf(portStr, "%d", &port)

		if httpsDefaultPorts[port] {
			urls = append(urls, fmt.Sprintf("https://%s:%d", host, port))
		} else if port == 80 {
			urls = append(urls, fmt.Sprintf("http://%s:%d", host, port))
		} else {
			// Try HTTP first, then HTTPS
			urls = append(urls, fmt.Sprintf("http://%s:%d", host, port))
			urls = append(urls, fmt.Sprintf("https://%s:%d", host, port))
		}
	}

	return urls
}

func splitHostPort(input string) (string, string, error) {
	// Handle IPv6 addresses
	if strings.HasPrefix(input, "[") {
		return net_SplitHostPort(input)
	}
	// Simple split for IPv4
	idx := strings.LastIndex(input, ":")
	if idx < 0 {
		return "", "", fmt.Errorf("no port in %q", input)
	}
	return input[:idx], input[idx+1:], nil
}

func net_SplitHostPort(s string) (string, string, error) {
	// Thin wrapper — avoid importing net just for this in the common case
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", "", fmt.Errorf("no port in %q", s)
	}
	return s[:i], s[i+1:], nil
}

func probeURL(ctx context.Context, client *http.Client, targetURL string) *probeResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "surfbot-agent/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	// Read up to 64KB
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	bodyStr := string(body)

	pr := &probeResult{
		URL:        resp.Request.URL.String(),
		StatusCode: resp.StatusCode,
		Server:     resp.Header.Get("Server"),
		Metadata:   map[string]interface{}{},
	}

	// Extract title
	pr.Title = ExtractTitle(bodyStr)

	// Build metadata
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

	// TLS info
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		cert := resp.TLS.PeerCertificates[0]
		pr.Metadata["tls_issuer"] = cert.Issuer.CommonName
		pr.Metadata["tls_expiry"] = cert.NotAfter.Format(time.RFC3339)
		pr.Metadata["tls_valid"] = time.Now().Before(cert.NotAfter) && time.Now().After(cert.NotBefore)
	}

	// Technology detection
	pr.TechList = DetectTechnologies(resp.Header, bodyStr)

	return pr
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
