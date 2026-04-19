package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/surfbot-io/surfbot-agent/internal/cli/apiclient"
)

// Exit codes used by the SCHED1.3b CLI. Stable across subcommands so
// scripts can branch deterministically.
const (
	ExitOK         = 0
	ExitRuntime    = 1 // network failure, I/O error, unknown 5xx
	ExitValidation = 2 // bad flags, bad JSON, 4xx other than 404/409
	ExitNotFound   = 3 // 404
	ExitConflict   = 4 // 409
)

// HandleAPIError prints err to stderr and returns the appropriate
// exit code for the CLI to bubble up. Shape of the output depends on
// format: table/human prints a short error line plus field errors,
// JSON emits the APIError as structured JSON so scripts can parse it.
//
// A nil err returns ExitOK without printing anything.
func HandleAPIError(err error, format OutputFormat, stderr io.Writer) int {
	if err == nil {
		return ExitOK
	}
	var apiErr *apiclient.APIError
	if !errors.As(err, &apiErr) {
		_, _ = fmt.Fprintln(stderr, err)
		return ExitRuntime
	}

	if format == FormatJSON {
		enc := json.NewEncoder(stderr)
		enc.SetIndent("", "  ")
		_ = enc.Encode(apiErr)
	} else {
		_, _ = fmt.Fprintln(stderr, apiErr)
	}
	return StatusToExitCode(apiErr.StatusCode)
}

// StatusToExitCode maps HTTP status to CLI exit code. 2xx/3xx
// collapse to ExitOK — callers that see those shouldn't have ended
// up here, but we pick the most permissive default. Anything in the
// 5xx range is a runtime error; 503 specifically is also runtime
// (dispatcher unreachable).
func StatusToExitCode(status int) int {
	switch {
	case status >= 200 && status < 400:
		return ExitOK
	case status == 404:
		return ExitNotFound
	case status == 409:
		return ExitConflict
	case status >= 400 && status < 500:
		return ExitValidation
	default:
		return ExitRuntime
	}
}
