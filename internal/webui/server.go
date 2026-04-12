package webui

import (
	"bytes"
	"embed"
	"fmt"
	"html"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/detection"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

//go:embed static/*
var staticFS embed.FS

// ServerOptions configures the HTTP server.
type ServerOptions struct {
	Bind     string
	Port     int
	Version  string
	Registry *detection.Registry // optional: enables scan triggering from web UI
	// Daemon is optional; when populated the /api/daemon/* routes expose
	// the agent status card and on-demand trigger (SPEC-X3.1). When nil
	// the routes still respond but always report `installed: false`.
	Daemon *DaemonView
	// AuthToken protects /api/*. When non-empty, every /api/* request must
	// supply Authorization: Bearer <token>. The same token is injected into
	// the SPA shell via a <meta name="surfbot-token"> tag so the frontend
	// can read it on load and pass it on every fetch. Empty token disables
	// the gate (used by handler unit tests).
	AuthToken string
}

// NewServer creates an HTTP server for the web UI dashboard.
// It returns the server and a listener that is already bound to the address.
// Use srv.Serve(ln) instead of srv.ListenAndServe() to avoid TOCTOU race.
func NewServer(store *storage.SQLiteStore, opts ServerOptions) (*http.Server, net.Listener, error) {
	mux := http.NewServeMux()
	h := &handler{store: store, version: opts.Version, registry: opts.Registry, daemon: opts.Daemon}

	// SPEC-X3.1 Agent card endpoints. Note the /api/daemon/* prefix —
	// these live outside /api/v1/ because they describe the daemon
	// process, not versioned domain data.
	mux.HandleFunc("/api/daemon/status", h.handleDaemonStatus)
	mux.HandleFunc("/api/daemon/trigger", h.handleDaemonTrigger)

	// Read-only API routes
	mux.HandleFunc("/api/v1/overview", h.handleOverview)
	mux.HandleFunc("/api/v1/assets/tree", h.handleAssetTree)
	mux.HandleFunc("/api/v1/assets", h.handleAssets)
	mux.HandleFunc("/api/v1/tools", h.handleTools)
	mux.HandleFunc("/api/v1/tools/available", h.handleAvailableTools)
	mux.HandleFunc("/api/v1/scans/status", h.handleScanStatus)

	// Findings: GET list, GET grouped, GET detail, PATCH status
	mux.HandleFunc("/api/v1/findings/grouped", h.handleFindingsGrouped)
	mux.HandleFunc("/api/v1/findings", h.handleFindings)
	mux.HandleFunc("/api/v1/findings/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			h.handleUpdateFindingStatus(w, r)
			return
		}
		h.handleFindingDetail(w, r)
	})

	// Targets: GET list, POST create, DELETE by id
	mux.HandleFunc("/api/v1/targets", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			h.handleTargets(w, r)
		case http.MethodPost:
			h.handleCreateTarget(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/targets/", h.handleDeleteTarget)

	// Schedule: GET config, PUT config
	mux.HandleFunc("/api/v1/schedule", h.handleSchedule)

	// Scans: GET list, GET detail, POST trigger
	mux.HandleFunc("/api/v1/scans", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			h.handleScans(w, r)
		case http.MethodPost:
			h.handleCreateScan(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/scans/", func(w http.ResponseWriter, r *http.Request) {
		// /api/v1/scans/status is handled above by the more specific route
		h.handleScanDetail(w, r)
	})

	// Static files with SPA fallback. The SPA shell (index.html) gets the
	// auth token injected as a <meta> tag at serve time so the frontend
	// can read it on load and forward it on every /api/* fetch.
	sub, _ := fs.Sub(staticFS, "static")
	fileServer := http.FileServer(http.FS(sub))
	indexHTML, _ := fs.ReadFile(sub, "index.html")
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" || path == "/index.html" {
			serveIndex(w, indexHTML, opts.AuthToken)
			return
		}

		cleanPath := strings.TrimPrefix(path, "/")
		if _, err := fs.Stat(sub, cleanPath); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}

		// SPA fallback: serve index.html for unknown *route* paths only.
		// Asset directories must 404 cleanly so that a typo'd image or
		// missing JS file does not silently return the HTML shell — that
		// would mask bugs and let MIME-sniffing edge cases bite us.
		if isAssetPath(path) {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, indexHTML, opts.AuthToken)
	})

	addr := fmt.Sprintf("%s:%d", opts.Bind, opts.Port)

	// Bind the port now to avoid TOCTOU race
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("port %d is already in use: %w", opts.Port, err)
	}

	// Build the middleware chain. Outermost is logging so every request
	// (including 421/403/401 rejects) ends up in the access log; innermost
	// is the mux. Order: log → headers → host → origin → token → mux.
	var handler http.Handler = mux
	if opts.AuthToken != "" {
		handler = requireToken(opts.AuthToken)(handler)
	}
	handler = validateOrigin(allowedOrigins(opts.Port))(handler)
	handler = validateHost(allowedHosts(opts.Port))(handler)
	handler = securityHeaders(handler)
	handler = loggingMiddleware(handler)

	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	return srv, ln, nil
}

// isAssetPath reports whether a request path lives under one of the
// static-asset directory prefixes the SPA serves. The SPA fallback skips
// these so that missing assets return 404 instead of HTML.
func isAssetPath(p string) bool {
	for _, pfx := range []string{"/static/", "/js/", "/css/", "/img/", "/assets/", "/fonts/"} {
		if strings.HasPrefix(p, pfx) {
			return true
		}
	}
	return false
}

// serveIndex writes index.html with a <meta name="surfbot-token"> tag
// injected into <head>. The token is HTML-escaped defensively even though
// it is hex-only by construction.
func serveIndex(w http.ResponseWriter, indexHTML []byte, token string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if token == "" {
		_, _ = w.Write(indexHTML)
		return
	}
	meta := []byte(`  <meta name="surfbot-token" content="` + html.EscapeString(token) + `">` + "\n")
	out := bytes.Replace(indexHTML, []byte("</head>"), append(meta, []byte("</head>")...), 1)
	_, _ = w.Write(out)
}

// loggingMiddleware logs each HTTP request.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		log.Printf("[webui] %s %s %d %s", r.Method, r.URL.Path, rw.status, time.Since(start).Round(time.Millisecond))
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
