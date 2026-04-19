package webui

// Tests for the SPA fallback restriction and HEAD support added in PR4.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsAssetPath(t *testing.T) {
	cases := map[string]bool{
		"/static/foo.png": true,
		"/js/app.js":      true,
		"/css/main.css":   true,
		"/img/logo.svg":   true,
		"/assets/x":       true,
		"/fonts/a.woff2":  true,
		"/":               false,
		"/findings":       false,
		"/scans/abc":      false,
		// /api/* is asset-like (returns 404 on miss) so unregistered
		// API paths never silently resolve to the SPA shell — see
		// SPEC-SCHED1.4a R11.
		"/api/v1/scans": true,
		"/api/daemon/x": true,
	}
	for p, want := range cases {
		if got := isAssetPath(p); got != want {
			t.Errorf("isAssetPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestHandleDaemonStatus_HEAD(t *testing.T) {
	h := &handler{daemon: nil}
	req := httptest.NewRequest(http.MethodHead, "/api/daemon/status", nil)
	rec := httptest.NewRecorder()
	h.handleDaemonStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body should be empty, got %d bytes", rec.Body.Len())
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" {
		t.Error("HEAD should set Content-Type")
	}
}

func TestHandleDaemonStatus_RejectsPUT(t *testing.T) {
	h := &handler{daemon: nil}
	req := httptest.NewRequest(http.MethodPut, "/api/daemon/status", nil)
	rec := httptest.NewRecorder()
	h.handleDaemonStatus(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT status = %d, want 405", rec.Code)
	}
}
