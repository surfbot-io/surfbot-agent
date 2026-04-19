package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/cli/apiclient"
)

type stubDefaultsClient struct {
	get        apiclient.ScheduleDefaults
	getErr     error
	updated    apiclient.ScheduleDefaults
	updateErr  error
	lastUpdate apiclient.UpdateScheduleDefaultsRequest
}

func (s *stubDefaultsClient) GetDefaults(ctx context.Context) (apiclient.ScheduleDefaults, error) {
	return s.get, s.getErr
}
func (s *stubDefaultsClient) UpdateDefaults(ctx context.Context, req apiclient.UpdateScheduleDefaultsRequest) (apiclient.ScheduleDefaults, error) {
	s.lastUpdate = req
	return s.updated, s.updateErr
}

func withStubDefaultsClient(t *testing.T, stub *stubDefaultsClient) {
	t.Helper()
	prev := defaultsClientFactory
	defaultsClientFactory = func(cmd *cobra.Command) (defaultsClient, error) { return stub, nil }
	t.Cleanup(func() { defaultsClientFactory = prev })
}

func TestDefaultsShow(t *testing.T) {
	withStubDefaultsClient(t, &stubDefaultsClient{
		get: apiclient.ScheduleDefaults{
			DefaultRRule: "FREQ=DAILY", DefaultTimezone: "UTC",
			MaxConcurrentScans: 4, JitterSeconds: 60,
		},
	})
	out, _, err := runCLI(t, "defaults", "show")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(out, "FREQ=DAILY") || !strings.Contains(out, "MaxConcurrentScans") {
		t.Fatalf("output: %s", out)
	}
}

func TestDefaultsUpdateMergesFields(t *testing.T) {
	stub := &stubDefaultsClient{
		get: apiclient.ScheduleDefaults{
			DefaultRRule: "FREQ=DAILY", DefaultTimezone: "UTC",
			MaxConcurrentScans: 4, JitterSeconds: 60,
		},
		updated: apiclient.ScheduleDefaults{
			DefaultRRule: "FREQ=WEEKLY", DefaultTimezone: "UTC",
			MaxConcurrentScans: 6, JitterSeconds: 60,
		},
	}
	withStubDefaultsClient(t, stub)
	_, _, err := runCLI(t, "defaults", "update",
		"--rrule", "FREQ=WEEKLY", "--max-concurrent-scans", "6")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	// Untouched fields preserved from GET.
	if stub.lastUpdate.DefaultTimezone != "UTC" {
		t.Fatalf("timezone not preserved: %+v", stub.lastUpdate)
	}
	if stub.lastUpdate.DefaultRRule != "FREQ=WEEKLY" {
		t.Fatalf("rrule not applied: %+v", stub.lastUpdate)
	}
	if stub.lastUpdate.MaxConcurrentScans != 6 {
		t.Fatalf("max_concurrent not applied: %+v", stub.lastUpdate)
	}
}

func TestDefaultsUpdateServerRejection(t *testing.T) {
	withStubDefaultsClient(t, &stubDefaultsClient{
		updateErr: &apiclient.APIError{
			StatusCode: http.StatusUnprocessableEntity,
			Title:      "Invalid defaults",
		},
	})
	_, _, err := runCLI(t, "defaults", "update", "--max-concurrent-scans", "0")
	var e errExit
	if !errors.As(err, &e) || int(e) != 2 {
		t.Fatalf("want exit 2, got %v", err)
	}
}
