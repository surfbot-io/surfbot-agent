package cli

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/cli/apiclient"
)

type stubAdhocClient struct {
	out       apiclient.CreateAdHocResponse
	err       error
	lastReq   apiclient.CreateAdHocRequest
	callCount int
}

func (s *stubAdhocClient) CreateAdHocScan(ctx context.Context, req apiclient.CreateAdHocRequest) (apiclient.CreateAdHocResponse, error) {
	s.callCount++
	s.lastReq = req
	return s.out, s.err
}

func withStubAdhocClient(t *testing.T, stub *stubAdhocClient) {
	t.Helper()
	prev := adhocScanClientFactory
	adhocScanClientFactory = func(cmd *cobra.Command) (adhocScanClient, error) { return stub, nil }
	t.Cleanup(func() { adhocScanClientFactory = prev })
}

func TestScanAdhocSuccess(t *testing.T) {
	stub := &stubAdhocClient{
		out: apiclient.CreateAdHocResponse{AdHocRunID: "ah1", ScanID: "s1"},
	}
	withStubAdhocClient(t, stub)
	out, _, err := runCLI(t, "scan", "adhoc", "--target", "t1")
	if err != nil {
		t.Fatalf("scan adhoc: %v", err)
	}
	if !strings.Contains(out, "ah1") || !strings.Contains(out, "s1") {
		t.Fatalf("output: %s", out)
	}
	if stub.lastReq.TargetID != "t1" {
		t.Fatalf("target not forwarded: %+v", stub.lastReq)
	}
	if stub.lastReq.ToolConfigOverride != nil {
		t.Fatalf("override must be nil when flag omitted, got %+v", stub.lastReq.ToolConfigOverride)
	}
}

func TestScanAdhocMissingTarget(t *testing.T) {
	withStubAdhocClient(t, &stubAdhocClient{})
	_, _, err := runCLI(t, "scan", "adhoc")
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("want required err, got %v", err)
	}
}

func TestScanAdhocTargetBusyExits4(t *testing.T) {
	withStubAdhocClient(t, &stubAdhocClient{
		err: &apiclient.APIError{
			StatusCode: http.StatusConflict,
			Title:      "Target busy",
			Type:       "/problems/target-busy",
		},
	})
	_, errOut, err := runCLI(t, "scan", "adhoc", "--target", "t1")
	var e errExit
	if !errors.As(err, &e) || int(e) != 4 {
		t.Fatalf("want exit 4, got %v", err)
	}
	if !strings.Contains(errOut, "Target busy") {
		t.Fatalf("stderr: %s", errOut)
	}
}

func TestScanAdhocDispatcherUnreachableExits1(t *testing.T) {
	withStubAdhocClient(t, &stubAdhocClient{
		err: &apiclient.APIError{
			StatusCode: http.StatusServiceUnavailable,
			Title:      "Dispatcher unreachable",
			Type:       "/problems/dispatcher-unreachable",
		},
	})
	_, _, err := runCLI(t, "scan", "adhoc", "--target", "t1")
	var e errExit
	if !errors.As(err, &e) || int(e) != 1 {
		t.Fatalf("want exit 1, got %v", err)
	}
}

func TestScanAdhocOverrideFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "override.json")
	if err := os.WriteFile(path, []byte(`{"nuclei":{"severity":["critical"]}}`), 0o600); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	stub := &stubAdhocClient{out: apiclient.CreateAdHocResponse{AdHocRunID: "ah1"}}
	withStubAdhocClient(t, stub)
	_, _, err := runCLI(t, "scan", "adhoc", "--target", "t1",
		"--tool-config-override", path)
	if err != nil {
		t.Fatalf("adhoc: %v", err)
	}
	if len(stub.lastReq.ToolConfigOverride) == 0 {
		t.Fatalf("override not forwarded: %+v", stub.lastReq.ToolConfigOverride)
	}
}

func TestScanAdhocWaitIsNoOpWithWarning(t *testing.T) {
	stub := &stubAdhocClient{out: apiclient.CreateAdHocResponse{AdHocRunID: "ah1"}}
	withStubAdhocClient(t, stub)
	_, errOut, err := runCLI(t, "scan", "adhoc", "--target", "t1", "--wait")
	if err != nil {
		t.Fatalf("adhoc --wait: %v", err)
	}
	if !strings.Contains(errOut, "--wait is a no-op") {
		t.Fatalf("missing warning on stderr: %s", errOut)
	}
	if stub.callCount != 1 {
		t.Fatalf("dispatch called %d times, want 1", stub.callCount)
	}
}
