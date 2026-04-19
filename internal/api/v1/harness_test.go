package v1

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// newTestStore opens a file-backed SQLite store in t.TempDir. Per the
// 1.2c lesson, modernc.org/sqlite's :memory: is per-connection and the
// *sql.DB pool defeats schema sharing — file backing guarantees every
// pool connection sees the same schema.
func newTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	s, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// newTestAPI wires an http.ServeMux with every v1 route and the supplied
// deps plugged in. Returned server is ready to serve requests; teardown
// is automatic via t.Cleanup.
func newTestAPI(t *testing.T, deps APIDeps) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	RegisterRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// defaultAPIDeps returns an APIDeps backed by `store` with every store
// wired up. The Expander, Blackouts, and Dispatcher fields are left nil
// so tests can exercise the fallback paths explicitly.
func defaultAPIDeps(store *storage.SQLiteStore) APIDeps {
	return APIDeps{
		Store:         store,
		ScheduleStore: store.Schedules(),
		TemplateStore: store.Templates(),
		BlackoutStore: store.Blackouts(),
		DefaultsStore: store.ScheduleDefaults(),
		AdHocStore:    store.AdHocScanRuns(),
	}
}

// seedTarget inserts a target and returns its ID. Convenience for the
// FK-required fields on schedules and ad-hoc runs.
func seedTarget(t *testing.T, store *storage.SQLiteStore, value string) string {
	t.Helper()
	tgt := &model.Target{Value: value}
	if err := store.CreateTarget(t.Context(), tgt); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	return tgt.ID
}

// doJSON wraps an httptest request lifecycle: marshals body, sets the
// JSON content type, and returns the raw response + body. Body is
// always closed so callers can ignore.
func doJSON(t *testing.T, srv *httptest.Server, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
		rdr = buf
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, respBody
}

// decode unmarshals a JSON body into v, failing the test on error.
func decode(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode body: %v\nbody: %s", err, body)
	}
}
