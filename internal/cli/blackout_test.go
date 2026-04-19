package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/cli/apiclient"
)

type stubBlackoutClient struct {
	list       apiclient.PaginatedResponse[apiclient.Blackout]
	listErr    error
	get        apiclient.Blackout
	getErr     error
	created    apiclient.Blackout
	createErr  error
	updated    apiclient.Blackout
	updateErr  error
	deleteErr  error
	lastCreate apiclient.CreateBlackoutRequest
}

func (s *stubBlackoutClient) ListBlackouts(ctx context.Context, at string, limit, offset int) (apiclient.PaginatedResponse[apiclient.Blackout], error) {
	return s.list, s.listErr
}
func (s *stubBlackoutClient) GetBlackout(ctx context.Context, id string) (apiclient.Blackout, error) {
	return s.get, s.getErr
}
func (s *stubBlackoutClient) CreateBlackout(ctx context.Context, req apiclient.CreateBlackoutRequest) (apiclient.Blackout, error) {
	s.lastCreate = req
	return s.created, s.createErr
}
func (s *stubBlackoutClient) UpdateBlackout(ctx context.Context, id string, req apiclient.UpdateBlackoutRequest) (apiclient.Blackout, error) {
	return s.updated, s.updateErr
}
func (s *stubBlackoutClient) DeleteBlackout(ctx context.Context, id string) error {
	return s.deleteErr
}

func withStubBlackoutClient(t *testing.T, stub *stubBlackoutClient) {
	t.Helper()
	prev := blackoutClientFactory
	blackoutClientFactory = func(cmd *cobra.Command) (blackoutClient, error) { return stub, nil }
	t.Cleanup(func() { blackoutClientFactory = prev })
}

func TestBlackoutList(t *testing.T) {
	withStubBlackoutClient(t, &stubBlackoutClient{
		list: apiclient.PaginatedResponse[apiclient.Blackout]{
			Items: []apiclient.Blackout{{
				ID: "b1", Name: "weekends", Scope: "global",
				RRule: "FREQ=WEEKLY;BYDAY=SA,SU", DurationSeconds: 24 * 3600, Enabled: true,
			}},
		},
	})
	out, _, err := runCLI(t, "blackout", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "weekends") {
		t.Fatalf("output: %s", out)
	}
}

func TestBlackoutCreateGlobal(t *testing.T) {
	stub := &stubBlackoutClient{created: apiclient.Blackout{ID: "b1", Scope: "global"}}
	withStubBlackoutClient(t, stub)
	_, _, err := runCLI(t, "blackout", "create",
		"--name", "wk", "--rrule", "FREQ=WEEKLY", "--duration", "8h")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if stub.lastCreate.Scope != "global" {
		t.Fatalf("expected global scope, got %q", stub.lastCreate.Scope)
	}
	if stub.lastCreate.DurationSeconds != 8*3600 {
		t.Fatalf("duration not converted: %d", stub.lastCreate.DurationSeconds)
	}
}

func TestBlackoutCreateTargetScope(t *testing.T) {
	stub := &stubBlackoutClient{created: apiclient.Blackout{ID: "b1", Scope: "target"}}
	withStubBlackoutClient(t, stub)
	_, _, err := runCLI(t, "blackout", "create",
		"--name", "wk", "--rrule", "FREQ=DAILY", "--duration", "1h",
		"--target-id", "tgt_xyz")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if stub.lastCreate.Scope != "target" {
		t.Fatalf("scope: %q", stub.lastCreate.Scope)
	}
	if stub.lastCreate.TargetID == nil || *stub.lastCreate.TargetID != "tgt_xyz" {
		t.Fatalf("target: %+v", stub.lastCreate.TargetID)
	}
}

func TestBlackoutCreateMissingFlags(t *testing.T) {
	withStubBlackoutClient(t, &stubBlackoutClient{})
	_, _, err := runCLI(t, "blackout", "create")
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("want required err, got %v", err)
	}
}
