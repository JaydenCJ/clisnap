// Package runner executes the command under test and captures its
// observable behavior: stdout, stderr and the exit code.
//
// Stdin is always connected to an empty stream so interactive prompts
// fail fast instead of hanging a test run, and stdout/stderr are captured
// separately because collapsing them would hide a class of regressions
// (output migrating between streams).
package runner

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
)

// Result is one command execution's captured behavior.
type Result struct {
	Exit   int
	Stdout string
	Stderr string
}

// Run executes argv and captures the result. A non-zero exit code is not
// an error — it is data, recorded in the snapshot and asserted on check.
// Run only fails when the process cannot be started at all (missing
// binary, permission denied), which callers surface as a hard error
// rather than a snapshot mismatch.
func Run(argv []string) (*Result, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = bytes.NewReader(nil) // empty stdin: never block on a prompt

	err := cmd.Run()
	res := &Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.Exit = exitErr.ExitCode()
			return res, nil
		}
		return nil, fmt.Errorf("run %q: %v", argv[0], err)
	}
	return res, nil
}
