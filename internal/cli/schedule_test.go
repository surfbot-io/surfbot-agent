package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/cli/apiclient"
)

// stubScheduleClient implements scheduleClient with tables for each
// method. Tests fill the relevant fields and leave the rest zero.
type stubScheduleClient struct {
	list       apiclient.PaginatedResponse[apiclient.Schedule]
	listErr    error
	get        apiclient.Schedule
	getErr     error
	created    apiclient.Schedule
	createErr  error
	updated    apiclient.Schedule
	updateErr  error
	deleteErr  error
	paused     apiclient.Schedule
	pauseErr   error
	resumed    apiclient.Schedule
	resumeErr  error
	upcoming   apiclient.UpcomingResponse
	upErr      error
	bulk       apiclient.BulkScheduleResponse
	bulkErr    error
	lastCreate apiclient.CreateScheduleRequest
	lastBulk   apiclient.BulkScheduleRequest
}

func (s *stubScheduleClient) ListSchedules(ctx context.Context, p apiclient.ListSchedulesParams) (apiclient.PaginatedResponse[apiclient.Schedule], error) {
	return s.list, s.listErr
}
func (s *stubScheduleClient) GetSchedule(ctx context.Context, id string) (apiclient.Schedule, error) {
	return s.get, s.getErr
}
func (s *stubScheduleClient) CreateSchedule(ctx context.Context, req apiclient.CreateScheduleRequest) (apiclient.Schedule, error) {
	s.lastCreate = req
	return s.created, s.createErr
}
func (s *stubScheduleClient) UpdateSchedule(ctx context.Context, id string, req apiclient.UpdateScheduleRequest) (apiclient.Schedule, error) {
	return s.updated, s.updateErr
}
func (s *stubScheduleClient) DeleteSchedule(ctx context.Context, id string) error {
	return s.deleteErr
}
func (s *stubScheduleClient) PauseSchedule(ctx context.Context, id string) (apiclient.Schedule, error) {
	return s.paused, s.pauseErr
}
func (s *stubScheduleClient) ResumeSchedule(ctx context.Context, id string) (apiclient.Schedule, error) {
	return s.resumed, s.resumeErr
}
func (s *stubScheduleClient) UpcomingSchedules(ctx context.Context, p apiclient.UpcomingParams) (apiclient.UpcomingResponse, error) {
	return s.upcoming, s.upErr
}
func (s *stubScheduleClient) BulkSchedules(ctx context.Context, req apiclient.BulkScheduleRequest) (apiclient.BulkScheduleResponse, error) {
	s.lastBulk = req
	return s.bulk, s.bulkErr
}

// withStubScheduleClient swaps scheduleClientFactory for the duration
// of a test. t.Cleanup restores the original so test ordering doesn't
// leak.
func withStubScheduleClient(t *testing.T, stub *stubScheduleClient) {
	t.Helper()
	prev := scheduleClientFactory
	scheduleClientFactory = func(cmd *cobra.Command) (scheduleClient, error) {
		return stub, nil
	}
	t.Cleanup(func() { scheduleClientFactory = prev })
}

// runScheduleCLI invokes rootCmd with the given args and captures
// stdout + stderr independently. Always clears rootCmd's state so
// tests don't bleed into each other.
func runScheduleCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	// Reset persistent flag state leaked by earlier tests (e.g.,
	// conformance_test.go passes --json against unrelated commands).
	prevJSON := jsonOut
	jsonOut = false
	_ = scheduleCmd.PersistentFlags().Set("output", "table")
	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		jsonOut = prevJSON
	})
	err := rootCmd.Execute()
	return out.String(), errBuf.String(), err
}

func TestScheduleListRendersTable(t *testing.T) {
	withStubScheduleClient(t, &stubScheduleClient{
		list: apiclient.PaginatedResponse[apiclient.Schedule]{
			Total: 1,
			Items: []apiclient.Schedule{{
				ID: "sched_12345", TargetID: "tgt_12345", Status: "active",
				RRule: "FREQ=DAILY",
			}},
		},
	})
	out, _, err := runScheduleCLI(t, "schedule", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "sched_1234") || !strings.Contains(out, "active") {
		t.Fatalf("output missing expected rows: %s", out)
	}
}

func TestScheduleCreateValidationMissingFlags(t *testing.T) {
	withStubScheduleClient(t, &stubScheduleClient{})
	_, _, err := runScheduleCLI(t, "schedule", "create")
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected required-flags error, got %v", err)
	}
}

func TestScheduleCreateAPIError(t *testing.T) {
	stub := &stubScheduleClient{
		createErr: &apiclient.APIError{
			StatusCode: http.StatusUnprocessableEntity,
			Title:      "Invalid RRULE",
			FieldErrors: []apiclient.FieldError{
				{Field: "rrule", Message: "missing FREQ"},
			},
		},
	}
	withStubScheduleClient(t, stub)
	_, errOut, err := runScheduleCLI(t, "schedule", "create",
		"--target", "t1", "--name", "n", "--rrule", "bogus", "--tzid", "UTC")
	// Exit 2 is expected.
	var e errExit
	if !errors.As(err, &e) || int(e) != 2 {
		t.Fatalf("exit code: %v", err)
	}
	if !strings.Contains(errOut, "Invalid RRULE") || !strings.Contains(errOut, "rrule") {
		t.Fatalf("stderr missing problem details: %s", errOut)
	}
}

func TestSchedulePauseResumeIdempotent(t *testing.T) {
	stub := &stubScheduleClient{
		paused:  apiclient.Schedule{ID: "s1", Status: "paused"},
		resumed: apiclient.Schedule{ID: "s1", Status: "active"},
	}
	withStubScheduleClient(t, stub)
	for _, name := range []string{"pause", "resume"} {
		out, _, err := runScheduleCLI(t, "schedule", name, "s1")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(out, "s1") {
			t.Fatalf("%s output: %s", name, out)
		}
	}
}

func TestScheduleDeleteRequiresConfirm(t *testing.T) {
	t.Setenv("SURFBOT_TEST", "")
	withStubScheduleClient(t, &stubScheduleClient{})
	_, _, err := runScheduleCLI(t, "schedule", "delete", "s1")
	var e errExit
	if !errors.As(err, &e) || int(e) != 2 {
		t.Fatalf("non-TTY delete without --force should exit 2, got %v", err)
	}
}

func TestScheduleDeleteWithForce(t *testing.T) {
	t.Setenv("SURFBOT_TEST", "")
	withStubScheduleClient(t, &stubScheduleClient{})
	out, _, err := runScheduleCLI(t, "schedule", "delete", "s1", "--force")
	if err != nil {
		t.Fatalf("delete --force: %v", err)
	}
	if !strings.Contains(out, "deleted s1") {
		t.Fatalf("missing deletion confirmation: %s", out)
	}
}

func TestScheduleUpcoming(t *testing.T) {
	stub := &stubScheduleClient{
		upcoming: apiclient.UpcomingResponse{
			Items: []apiclient.UpcomingFiring{{ScheduleID: "s1", TargetID: "t1"}},
		},
	}
	withStubScheduleClient(t, stub)
	out, _, err := runScheduleCLI(t, "schedule", "upcoming")
	if err != nil {
		t.Fatalf("upcoming: %v", err)
	}
	if !strings.Contains(out, "s1") {
		t.Fatalf("output: %s", out)
	}
}

func TestScheduleBulkRejectsUnknownOp(t *testing.T) {
	withStubScheduleClient(t, &stubScheduleClient{})
	_, _, err := runScheduleCLI(t, "schedule", "bulk", "lolwat", "s1")
	var e errExit
	if !errors.As(err, &e) {
		t.Fatalf("expected errExit, got %v", err)
	}
}

func TestScheduleBulkPause(t *testing.T) {
	stub := &stubScheduleClient{
		bulk: apiclient.BulkScheduleResponse{
			Operation: "pause", Succeeded: []string{"s1", "s2"},
		},
	}
	withStubScheduleClient(t, stub)
	out, _, err := runScheduleCLI(t, "schedule", "bulk", "pause", "s1", "s2")
	if err != nil {
		t.Fatalf("bulk pause: %v", err)
	}
	if !strings.Contains(out, "succeeded: 2") {
		t.Fatalf("output: %s", out)
	}
	if len(stub.lastBulk.ScheduleIDs) != 2 {
		t.Fatalf("expected 2 ids in request, got %+v", stub.lastBulk.ScheduleIDs)
	}
}

func TestScheduleListJSONOutput(t *testing.T) {
	withStubScheduleClient(t, &stubScheduleClient{
		list: apiclient.PaginatedResponse[apiclient.Schedule]{
			Total: 1,
			Items: []apiclient.Schedule{{ID: "s1", Status: "active"}},
		},
	})
	out, _, err := runScheduleCLI(t, "schedule", "list", "-o", "json")
	if err != nil {
		t.Fatalf("json list: %v", err)
	}
	if !strings.Contains(out, `"id": "s1"`) {
		t.Fatalf("json output: %s", out)
	}
}
