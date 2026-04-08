package webui

import (
	"embed"
	"fmt"
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

	// Findings: GET list, GET detail, PATCH status
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

	// Static files with SPA fallback
	sub, _ := fs.Sub(staticFS, "static")
	fileServer := http.FileServer(http.FS(sub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}

		cleanPath := strings.TrimPrefix(path, "/")
		if _, err := fs.Stat(sub, cleanPath); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}

		// SPA fallback: serve index.html for unknown paths
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf("%s:%d", opts.Bind, opts.Port)

	// Bind the port now to avoid TOCTOU race
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("port %d is already in use: %w", opts.Port, err)
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      loggingMiddleware(mux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	return srv, ln, nil
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
