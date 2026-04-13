package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// LoadOrCreateUIToken returns the loopback bearer token used to authenticate
// requests against /api/*. If <stateDir>/ui.token exists it is reused so the
// SPA shell does not have to reload across UI restarts; otherwise a fresh
// 32-byte hex token is generated and written with 0600 permissions.
//
// stateDir must already exist (callers should EnsureDirs first).
func LoadOrCreateUIToken(stateDir string) (string, error) {
	if stateDir == "" {
		return "", errors.New("ui token: empty state dir")
	}
	path := filepath.Join(stateDir, "ui.token")
	if data, err := os.ReadFile(path); err == nil {
		token := strings.TrimSpace(string(data))
		if token != "" {
			return token, nil
		}
	}
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("ui token: generating random bytes: %w", err)
	}
	token := hex.EncodeToString(buf[:])
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("ui token: writing %s: %w", path, err)
	}
	return token, nil
}

// allowedHosts returns the Host header values that are accepted for the
// loopback UI on the given port. We accept both the IPv4 literal and the
// "localhost" alias because browsers and curl users reach for either.
func allowedHosts(port int) []string {
	return []string{
		fmt.Sprintf("127.0.0.1:%d", port),
		fmt.Sprintf("localhost:%d", port),
	}
}

// allowedOrigins returns the Origin/Referer URL prefixes that are accepted
// for state-changing requests on the loopback UI.
func allowedOrigins(port int) []string {
	return []string{
		fmt.Sprintf("http://127.0.0.1:%d", port),
		fmt.Sprintf("http://localhost:%d", port),
	}
}

// securityHeaders sets the baseline response headers on every response.
// Cache-Control: no-store is added for /api/* so JSON snapshots are never
// cached by intermediaries (or by the browser tab).
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self'; " +
		"img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; " +
		"base-uri 'none'; form-action 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", csp)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// validateHost rejects requests whose Host header is not one of the
// allow-listed loopback values. This is the anti-DNS-rebinding gate: even
// though the listener binds to 127.0.0.1 the kernel still accepts a request
// whose Host says "evil.example", and the browser would happily make that
// request after a rebind. 421 Misdirected Request tells well-behaved
// clients to retry with the right host.
func validateHost(allowed []string) func(http.Handler) http.Handler {
	set := make(map[string]struct{}, len(allowed))
	for _, h := range allowed {
		set[h] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := set[r.Host]; !ok {
				http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// validateOrigin enforces a same-origin check on every state-changing
// request. POST/PUT/DELETE/PATCH must carry an Origin (or, failing that, a
// Referer) header that points back at the loopback UI; anything else is
// treated as cross-site and rejected with 403.
func validateOrigin(allowed []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				if !originAllowed(origin, allowed) {
					http.Error(w, "forbidden origin", http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if referer := r.Header.Get("Referer"); referer != "" {
				if !refererAllowed(referer, allowed) {
					http.Error(w, "forbidden referer", http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "missing origin", http.StatusForbidden)
		})
	}
}

func originAllowed(origin string, allowed []string) bool {
	for _, a := range allowed {
		if origin == a {
			return true
		}
	}
	return false
}

func refererAllowed(referer string, allowed []string) bool {
	for _, a := range allowed {
		if referer == a || strings.HasPrefix(referer, a+"/") {
			return true
		}
	}
	return false
}

// requireToken protects the /api/* surface with a constant-time bearer
// token check. The SPA shell receives the token via the meta tag injected
// into index.html and forwards it on every fetch (see static/js/api.js).
// Static assets and the SPA shell itself are intentionally not gated.
func requireToken(token string) func(http.Handler) http.Handler {
	wantHeader := []byte("Bearer " + token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}
			got := r.Header.Get("Authorization")
			if got == "" || subtle.ConstantTimeCompare([]byte(got), wantHeader) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
