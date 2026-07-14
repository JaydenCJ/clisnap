// Tests for the command runner. All commands are POSIX-shell builtins or
// tiny scripts, deterministic and offline; no PATH assumptions beyond sh.
package runner

import (
	"strings"
	"testing"
)

func TestRunCapturesStdout(t *testing.T) {
	res, err := Run([]string{"sh", "-c", "echo hello"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stdout != "hello\n" || res.Stderr != "" || res.Exit != 0 {
		t.Fatalf("got %+v", res)
	}
}

func TestRunKeepsStreamsSeparate(t *testing.T) {
	// Merging streams would hide output migrating from stdout to stderr,
	// which is a real regression (breaks pipelines).
	res, err := Run([]string{"sh", "-c", "echo out; echo err >&2"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stdout != "out\n" || res.Stderr != "err\n" {
		t.Fatalf("got stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
}

func TestRunReportsExitCodeAsDataNotError(t *testing.T) {
	res, err := Run([]string{"sh", "-c", "echo failing; exit 3"})
	if err != nil {
		t.Fatalf("nonzero exit must not be an error, got %v", err)
	}
	if res.Exit != 3 || res.Stdout != "failing\n" {
		t.Fatalf("got %+v", res)
	}
}

func TestRunMissingBinaryIsHardError(t *testing.T) {
	_, err := Run([]string{"definitely-not-a-real-binary-4f2a"})
	if err == nil || !strings.Contains(err.Error(), "definitely-not-a-real-binary-4f2a") {
		t.Fatalf("err = %v, want start failure naming the binary", err)
	}
}

func TestRunEmptyArgvErrors(t *testing.T) {
	if _, err := Run(nil); err == nil {
		t.Fatal("empty argv accepted")
	}
}

func TestRunStdinIsEmptyNotATerminal(t *testing.T) {
	// 'cat' must see immediate EOF and exit — a runner that inherits the
	// parent's stdin would hang every interactive-ish command.
	res, err := Run([]string{"cat"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stdout != "" || res.Exit != 0 {
		t.Fatalf("got %+v", res)
	}
}

func TestRunPreservesRawBytesAndMissingNewline(t *testing.T) {
	res, err := Run([]string{"sh", "-c", "printf 'no newline'"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stdout != "no newline" {
		t.Fatalf("got %q", res.Stdout)
	}
}
