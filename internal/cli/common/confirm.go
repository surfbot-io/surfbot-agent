package common

import (
	"bufio"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ConfirmDestructive prompts the user to type `yes` to confirm a
// destructive operation. Returns true if:
//
//   - force is true (operator passed --force / -y)
//   - SURFBOT_TEST=1 env var is set (used by CLI tests so prompts
//     never block CI)
//
// Returns false immediately without consuming stdin when:
//
//   - stdin is not a TTY (pipe / script)
//   - the user types anything other than "yes"
//
// The returned bool is the go/no-go signal; the caller decides what
// to do on false (typically: print an error and exit 2).
//
// in and out are injected so tests can verify the prompt text.
func ConfirmDestructive(in io.Reader, out io.Writer, prompt string, force bool) bool {
	if force {
		return true
	}
	if os.Getenv("SURFBOT_TEST") == "1" {
		return true
	}
	// Non-TTY: refuse silently. Scripts that want to proceed must pass
	// --force explicitly — this closes the "surfbot delete piped into
	// some log pipeline accidentally destroyed things" footgun.
	if !isTerminal(in) {
		return false
	}
	_, _ = out.Write([]byte(prompt))
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(scanner.Text()), "yes")
}

// isTerminal reports whether r is a TTY. Falls back to false for
// anything that isn't an *os.File (e.g., the bytes.Reader tests use).
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
