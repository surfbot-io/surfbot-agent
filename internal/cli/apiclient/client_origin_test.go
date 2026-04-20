//go:build integration

package apiclient_test

// SPEC-SCHED1-HOTFIX R3: prove the apiclient works end-to-end against
// the full webui middleware stack, not just the bare apiv1 mux.
//
// Before this hotfix, every POST/PUT/DELETE/PATCH from the CLI got 403
// "missing origin" because the webui's validateOrigin middleware
// enforces a same-origin check and the client never set the Origin
// header. Unit tests in this package hit httptest.NewServer(handler)
// and so bypassed that middleware entirely. This test is the missing
// harness-layer check: it drives GET + POST + DELETE through
// webui.NewServer (which wires securityHeaders → validateHost →
// validateOrigin → mux) and asserts that every write verb returns 2xx.
//
// Run with: go test -tags=integration ./internal/cli/apiclient/... -race -count=1

import (
	"context"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/cli/apiclient"
	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
	"github.com/surfbot-io/surfbot-agent/internal/webui"
)

// startWebuiServer boots a real webui.NewServer on a loopback port with
// a file-backed SQLite store. Mirrors the pattern used in
// internal/webui/ui_integration_test.go — file-backed :memory: isn't
// viable across the pool's multiple connections.
func startWebuiServer(t *testing.T) (*apiclient.Client, *storage.SQLiteStore, func()) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "hotfix.db"))
	require.NoError(t, err)

	tmpLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := tmpLn.Addr().(*net.TCPAddr).Port
	require.NoError(t, tmpLn.Close())

	srv, ln, err := webui.NewServer(store, webui.ServerOptions{
		Bind: "127.0.0.1", Port: port, Version: "hotfix-test",
	})
	require.NoError(t, err)

	go func() { _ = srv.Serve(ln) }()
	// Tiny settle so the listener is serving before the first request.
	time.Sleep(20 * time.Millisecond)

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	client := apiclient.New(base)

	cleanup := func() {
		_ = srv.Shutdown(context.Background())
		_ = store.Close()
	}
	return client, store, cleanup
}

// TestClientOrigin_FullStack_GetPostDelete drives one GET, one POST,
// and one DELETE through the full webui middleware chain. Pre-R1 this
// failed with 403 "missing origin" on POST and DELETE; post-R1 every
// call returns 2xx because the client now derives Origin from baseURL
// and sets it on every request.
func TestClientOrigin_FullStack_GetPostDelete(t *testing.T) {
	client, store, stop := startWebuiServer(t)
	defer stop()

	ctx := context.Background()

	// Seed a target so schedule create has a valid FK referent.
	tgt := &model.Target{Value: "example.com"}
	require.NoError(t, store.CreateTarget(ctx, tgt))

	// GET: list schedules. Safe methods pre-R1 already worked because
	// validateOrigin only gates mutating verbs — but we exercise it so
	// the full round-trip is proven (auth + host + origin + mux).
	listed, err := client.ListSchedules(ctx, apiclient.ListSchedulesParams{})
	require.NoError(t, err, "GET /api/v1/schedules")
	assert.Empty(t, listed.Items, "seed DB must start empty")

	// POST: create a schedule. Pre-R1 this 403'd with "missing origin".
	enabled := true
	created, err := client.CreateSchedule(ctx, apiclient.CreateScheduleRequest{
		TargetID: tgt.ID,
		Name:     "hotfix-schedule",
		RRule:    "FREQ=DAILY",
		Timezone: "UTC",
		Enabled:  &enabled,
	})
	require.NoError(t, err, "POST /api/v1/schedules must succeed through webui stack")
	require.NotEmpty(t, created.ID, "created schedule has empty ID")

	// DELETE: hard delete the row. Pre-R1 this 403'd likewise.
	err = client.DeleteSchedule(ctx, created.ID)
	require.NoError(t, err, "DELETE /api/v1/schedules/{id} must succeed through webui stack")

	// Sanity: the row is gone.
	after, err := client.ListSchedules(ctx, apiclient.ListSchedulesParams{})
	require.NoError(t, err)
	assert.Empty(t, after.Items)
}
