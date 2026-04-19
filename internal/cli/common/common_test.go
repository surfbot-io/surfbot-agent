package common

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/surfbot-io/surfbot-agent/internal/cli/apiclient"
)

func TestStatusToExitCode(t *testing.T) {
	cases := []struct {
		status int
		want   int
	}{
		{http.StatusOK, ExitOK},
		{http.StatusBadRequest, ExitValidation},
		{http.StatusUnprocessableEntity, ExitValidation},
		{http.StatusNotFound, ExitNotFound},
		{http.StatusConflict, ExitConflict},
		{http.StatusInternalServerError, ExitRuntime},
		{http.StatusServiceUnavailable, ExitRuntime},
	}
	for _, tc := range cases {
		got := StatusToExitCode(tc.status)
		if got != tc.want {
			t.Errorf("status=%d got=%d want=%d", tc.status, got, tc.want)
		}
	}
}

func TestHandleAPIErrorNilReturnsOK(t *testing.T) {
	var buf bytes.Buffer
	if c := HandleAPIError(nil, FormatTable, &buf); c != ExitOK {
		t.Fatalf("nil err should be exit 0, got %d", c)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no output, got %q", buf.String())
	}
}

func TestHandleAPIErrorUnknownGoesRuntime(t *testing.T) {
	var buf bytes.Buffer
	c := HandleAPIError(errors.New("boom"), FormatTable, &buf)
	if c != ExitRuntime {
		t.Fatalf("plain error → exit %d, want %d", c, ExitRuntime)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Fatalf("stderr missing error text: %q", buf.String())
	}
}

func TestHandleAPIErrorJSONShape(t *testing.T) {
	var buf bytes.Buffer
	err := &apiclient.APIError{
		StatusCode: http.StatusConflict,
		Type:       "/problems/target-busy",
		Title:      "busy",
		Status:     http.StatusConflict,
	}
	c := HandleAPIError(err, FormatJSON, &buf)
	if c != ExitConflict {
		t.Fatalf("got exit %d want %d", c, ExitConflict)
	}
	if !strings.Contains(buf.String(), `"type": "/problems/target-busy"`) {
		t.Fatalf("json body missing type: %q", buf.String())
	}
}

func TestParseOutputFormat(t *testing.T) {
	cases := map[string]OutputFormat{
		"":      FormatTable,
		"table": FormatTable,
		"JSON":  FormatJSON,
		"yaml":  FormatYAML,
		"yml":   FormatYAML,
	}
	for in, want := range cases {
		got, err := ParseOutputFormat(in)
		if err != nil || got != want {
			t.Errorf("ParseOutputFormat(%q) = %v %q, want %q", in, err, got, want)
		}
	}
	if _, err := ParseOutputFormat("xml"); err == nil {
		t.Errorf("expected error for xml")
	}
}

func TestEllipsize(t *testing.T) {
	cases := []struct{ in string; max int; want string }{
		{"short", 10, "short"},
		{"1234567890", 5, "1234…"},
		{"á́é", 2, "á…"},
		{"anything", 0, ""},
	}
	for _, tc := range cases {
		got := Ellipsize(tc.in, tc.max)
		if got != tc.want {
			t.Errorf("Ellipsize(%q,%d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}

func TestConfirmDestructiveForceShortCircuits(t *testing.T) {
	in := strings.NewReader("no\n")
	var out bytes.Buffer
	if !ConfirmDestructive(in, &out, "delete?", true) {
		t.Fatalf("force should short-circuit to true")
	}
	if out.Len() != 0 {
		t.Fatalf("no prompt should be emitted with force: %q", out.String())
	}
}

func TestConfirmDestructiveNonTTYReturnsFalse(t *testing.T) {
	// strings.Reader is not an *os.File, so isTerminal returns false.
	in := strings.NewReader("yes\n")
	var out bytes.Buffer
	// Ensure SURFBOT_TEST isn't tripping us.
	t.Setenv("SURFBOT_TEST", "")
	if ConfirmDestructive(in, &out, "delete?", false) {
		t.Fatalf("non-TTY without force should return false")
	}
}

func TestConfirmDestructiveTestEnv(t *testing.T) {
	t.Setenv("SURFBOT_TEST", "1")
	in := strings.NewReader("")
	var out bytes.Buffer
	if !ConfirmDestructive(in, &out, "delete?", false) {
		t.Fatalf("SURFBOT_TEST=1 should auto-confirm")
	}
}

func TestResolveAPIConfigFlagWins(t *testing.T) {
	t.Setenv("SURFBOT_DAEMON_URL", "http://env:1")
	cfg := ResolveAPIConfig("http://flag:1")
	if cfg.BaseURL != "http://flag:1" {
		t.Fatalf("base url = %q", cfg.BaseURL)
	}
}

func TestResolveAPIConfigEnvWinsOverDefault(t *testing.T) {
	t.Setenv("SURFBOT_DAEMON_URL", "http://env:7")
	cfg := ResolveAPIConfig("")
	if cfg.BaseURL != "http://env:7" {
		t.Fatalf("base url = %q", cfg.BaseURL)
	}
}

func TestResolveAPIConfigDefault(t *testing.T) {
	t.Setenv("SURFBOT_DAEMON_URL", "")
	cfg := ResolveAPIConfig("")
	if cfg.BaseURL != DefaultDaemonURL {
		t.Fatalf("base url = %q, want %q", cfg.BaseURL, DefaultDaemonURL)
	}
}

func TestRenderJSONYAMLTable(t *testing.T) {
	var buf bytes.Buffer
	payload := map[string]any{"a": 1}

	buf.Reset()
	if err := Render(&buf, FormatJSON, payload, nil); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !strings.Contains(buf.String(), `"a": 1`) {
		t.Fatalf("json body: %q", buf.String())
	}

	buf.Reset()
	if err := Render(&buf, FormatYAML, payload, nil); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	if !strings.Contains(buf.String(), "a: 1") {
		t.Fatalf("yaml body: %q", buf.String())
	}

	buf.Reset()
	called := false
	err := Render(&buf, FormatTable, payload, func(w io.Writer) error {
		called = true
		_, _ = w.Write([]byte("TBL"))
		return nil
	})
	if err != nil || !called || buf.String() != "TBL" {
		t.Fatalf("table renderer: called=%v body=%q err=%v", called, buf.String(), err)
	}
}

// Guard against signature drift on FormatTime — it accepts any type
// with IsZero and String methods.
func TestFormatTimeZero(t *testing.T) {
	got := FormatTime(zeroTime{})
	if got != "—" {
		t.Fatalf("zero formatting: %q", got)
	}
}

type zeroTime struct{}

func (zeroTime) IsZero() bool   { return true }
func (zeroTime) String() string { return "zero" }

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}