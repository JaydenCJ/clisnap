// Integration tests for the CLI, run in-process through cli.Run with
// injected streams. Every command under test is a POSIX-shell one-liner
// or a file inside t.TempDir(), so the suite is deterministic, offline,
// and independent of the working directory.
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaydenCJ/clisnap/internal/version"
)

// run invokes the CLI in-process and returns (exit code, stdout, stderr).
func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// snapDir returns a fresh snapshot dir path (not yet created).
func snapDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".clisnap")
}

// writeScript drops an executable shell script into a temp dir.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tool.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVersionFlagAndSubcommandAgree(t *testing.T) {
	code, a, _ := run(t, "", "--version")
	if code != 0 || a != "clisnap "+version.Version+"\n" {
		t.Fatalf("code=%d out=%q", code, a)
	}
	_, b, _ := run(t, "", "version")
	if a != b {
		t.Fatalf("flag %q != subcommand %q", a, b)
	}
}

func TestHelpListsEveryCommand(t *testing.T) {
	code, out, _ := run(t, "", "--help")
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	for _, cmd := range []string{"record", "check", "list", "show", "rm", "redact"} {
		if !strings.Contains(out, cmd) {
			t.Fatalf("help missing %q:\n%s", cmd, out)
		}
	}
}

func TestUsageErrorsExitTwoWithGuidance(t *testing.T) {
	// Every user mistake must produce exit 2 (never a false "mismatch")
	// and a message that points at the actual problem.
	dir := snapDir(t)
	cases := []struct {
		desc    string
		args    []string
		wantErr string
	}{
		{"no arguments", nil, "Usage"},
		{"unknown command", []string{"frobnicate"}, "frobnicate"},
		{"unknown global flag", []string{"--bogus", "list"}, "--bogus"},
		{"record without -- separator",
			[]string{"--dir", dir, "record", "greet", "sh", "-c", "echo hi"}, "usage"},
		{"record with path-traversal name",
			[]string{"--dir", dir, "record", "../evil", "--", "sh", "-c", "echo hi"},
			"invalid snapshot name"},
		{"record with unknown redactor",
			[]string{"--dir", dir, "record", "--redact", "nope", "x", "--", "sh", "-c", "echo hi"},
			"nope"},
		{"show without a snapshot", []string{"--dir", dir, "show", "ghost"}, `"ghost"`},
		{"rm without a snapshot", []string{"--dir", dir, "rm", "ghost"}, `"ghost"`},
		{"list with stray arguments", []string{"--dir", dir, "list", "extra"}, "usage"},
	}
	for _, c := range cases {
		code, _, errOut := run(t, "", c.args...)
		if code != 2 || !strings.Contains(errOut, c.wantErr) {
			t.Errorf("%s: code=%d stderr=%q, want exit 2 mentioning %q",
				c.desc, code, errOut, c.wantErr)
		}
	}
}

func TestRecordCreatesSnapshotFile(t *testing.T) {
	dir := snapDir(t)
	// --dir=X (equals form) is exercised here; the space form everywhere else.
	code, out, errOut := run(t, "", "--dir="+dir,
		"record", "greet", "--", "sh", "-c", "echo hello")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if !strings.Contains(out, "recorded greet") {
		t.Fatalf("out=%q", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "greet.snap"))
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	if !strings.Contains(string(data), "|hello") {
		t.Fatalf("snapshot content:\n%s", data)
	}
}

func TestRecordRedactsVolatileOutput(t *testing.T) {
	dir := snapDir(t)
	script := writeScript(t, `echo "pid $$ at 2026-07-12T16:09:33Z"`)
	code, out, errOut := run(t, "", "--dir", dir, "record", "vol", "--", script)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "vol.snap"))
	if !strings.Contains(string(data), "pid <PID> at <TIMESTAMP>") {
		t.Fatalf("volatile output not redacted:\n%s", data)
	}
	// The summary reports how much was redacted, so users can spot both
	// over- and under-redaction at record time.
	if !strings.Contains(out, "2 redactions") {
		t.Fatalf("out=%q", out)
	}
}

func TestRecordOverwriteNeedsForce(t *testing.T) {
	dir := snapDir(t)
	run(t, "", "--dir", dir, "record", "greet", "--", "sh", "-c", "echo one")
	code, _, errOut := run(t, "", "--dir", dir,
		"record", "greet", "--", "sh", "-c", "echo two")
	if code != 2 || !strings.Contains(errOut, "already exists") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	code, _, errOut = run(t, "", "--dir", dir,
		"record", "--force", "greet", "--", "sh", "-c", "echo two")
	if code != 0 {
		t.Fatalf("forced record: code=%d stderr=%q", code, errOut)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "greet.snap"))
	if !strings.Contains(string(data), "|two") {
		t.Fatalf("snapshot not replaced:\n%s", data)
	}
}

func TestRecordMissingBinaryFails(t *testing.T) {
	code, _, errOut := run(t, "", "--dir", snapDir(t),
		"record", "x", "--", "no-such-binary-9c1d")
	if code != 2 || !strings.Contains(errOut, "no-such-binary-9c1d") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestRecordCapturesNonZeroExitAsData(t *testing.T) {
	dir := snapDir(t)
	code, out, errOut := run(t, "", "--dir", dir,
		"record", "fails", "--", "sh", "-c", "echo doomed >&2; exit 4")
	if code != 0 {
		t.Fatalf("recording a failing command must succeed, code=%d stderr=%q", code, errOut)
	}
	if !strings.Contains(out, "exit 4") {
		t.Fatalf("out=%q", out)
	}
}

func TestRecordShellModeJoinsWords(t *testing.T) {
	dir := snapDir(t)
	code, _, errOut := run(t, "", "--dir", dir,
		"record", "--shell", "pipe", "--", "echo hello | tr a-z A-Z")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "pipe.snap"))
	if !strings.Contains(string(data), "|HELLO") {
		t.Fatalf("pipeline did not run through the shell:\n%s", data)
	}
}

func TestRecordRedactNoneKeepsOutputVerbatim(t *testing.T) {
	dir := snapDir(t)
	run(t, "", "--dir", dir, "record", "--redact", "none", "raw",
		"--", "sh", "-c", "echo pid 123 at 16:09:33")
	data, _ := os.ReadFile(filepath.Join(dir, "raw.snap"))
	if !strings.Contains(string(data), "|pid 123 at 16:09:33") {
		t.Fatalf("output was redacted despite 'none':\n%s", data)
	}
	if !strings.Contains(string(data), "redact: -") {
		t.Fatalf("empty redactor set not marked:\n%s", data)
	}
}

func TestCheckPassesOnStableCommand(t *testing.T) {
	dir := snapDir(t)
	run(t, "", "--dir", dir, "record", "greet", "--", "sh", "-c", "echo hello")
	code, out, errOut := run(t, "", "--dir", dir, "check", "greet")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q out=%q", code, errOut, out)
	}
	if !strings.Contains(out, "ok      greet") || !strings.Contains(out, "1 snapshot: 1 ok") {
		t.Fatalf("out=%q", out)
	}
}

func TestCheckPassesAcrossVolatileReruns(t *testing.T) {
	// The differentiator in one test: the command prints a fresh PID and
	// timestamp on every run, and check still passes because both record
	// and check pass output through the same redaction engine.
	dir := snapDir(t)
	script := writeScript(t, `echo "run pid=$$ at $(date -u +%Y-%m-%dT%H:%M:%SZ)"`)
	run(t, "", "--dir", dir, "record", "vol", "--", script)
	code, out, _ := run(t, "", "--dir", dir, "check", "vol")
	if code != 0 || !strings.Contains(out, "ok      vol") {
		t.Fatalf("volatile rerun failed: code=%d out=%q", code, out)
	}
}

func TestCheckFailsOnChangedOutputWithDiff(t *testing.T) {
	dir := snapDir(t)
	stateFile := filepath.Join(t.TempDir(), "state.txt")
	os.WriteFile(stateFile, []byte("original\n"), 0o644)
	run(t, "", "--dir", dir, "record", "state", "--", "cat", stateFile)

	os.WriteFile(stateFile, []byte("mutated\n"), 0o644)
	code, out, _ := run(t, "", "--dir", dir, "check", "state")
	if code != 1 {
		t.Fatalf("code=%d, want 1", code)
	}
	for _, want := range []string{"FAIL    state", "-original", "+mutated", "@@"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestCheckReportsExitCodeMismatch(t *testing.T) {
	dir := snapDir(t)
	flagFile := filepath.Join(t.TempDir(), "flag")
	os.WriteFile(flagFile, []byte("x"), 0o644)
	// Exits 0 while the flag file exists, 1 after it is removed.
	run(t, "", "--dir", dir, "record", "gate", "--", "sh", "-c", "test -e "+flagFile)
	os.Remove(flagFile)
	code, out, _ := run(t, "", "--dir", dir, "check", "gate")
	if code != 1 || !strings.Contains(out, "exit code: snapshot 0, current 1") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestCheckUpdateRewritesOnlyFailingSnapshots(t *testing.T) {
	dir := snapDir(t)
	stateFile := filepath.Join(t.TempDir(), "state.txt")
	os.WriteFile(stateFile, []byte("v1\n"), 0o644)
	run(t, "", "--dir", dir, "record", "state", "--", "cat", stateFile)
	run(t, "", "--dir", dir, "record", "greet", "--", "sh", "-c", "echo hi")
	greetBefore, _ := os.ReadFile(filepath.Join(dir, "greet.snap"))

	os.WriteFile(stateFile, []byte("v2\n"), 0o644)
	code, out, _ := run(t, "", "--dir", dir, "check", "--update")
	if code != 0 || !strings.Contains(out, "updated state") || !strings.Contains(out, "ok      greet") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	// The failing snapshot now passes a plain check...
	code, out, _ = run(t, "", "--dir", dir, "check", "state")
	if code != 0 || !strings.Contains(out, "ok      state") {
		t.Fatalf("post-update check: code=%d out=%q", code, out)
	}
	// ...and the passing one was not rewritten (no churn in git status).
	greetAfter, _ := os.ReadFile(filepath.Join(dir, "greet.snap"))
	if !bytes.Equal(greetBefore, greetAfter) {
		t.Fatal("passing snapshot was rewritten")
	}
}

func TestCheckAllRunsEverySnapshotSorted(t *testing.T) {
	dir := snapDir(t)
	run(t, "", "--dir", dir, "record", "bravo", "--", "sh", "-c", "echo b")
	run(t, "", "--dir", dir, "record", "alpha", "--", "sh", "-c", "echo a")
	code, out, _ := run(t, "", "--dir", dir, "check")
	if code != 0 {
		t.Fatalf("code=%d out=%q", code, out)
	}
	ia, ib := strings.Index(out, "ok      alpha"), strings.Index(out, "ok      bravo")
	if ia < 0 || ib < 0 || ia > ib {
		t.Fatalf("expected sorted results, out=%q", out)
	}
	if !strings.Contains(out, "2 snapshots: 2 ok") {
		t.Fatalf("summary wrong: %q", out)
	}
}

func TestCheckEmptyStoreFailsLoudly(t *testing.T) {
	// A check-all over zero snapshots passing would green-light a CI job
	// whose snapshots were never committed.
	code, _, errOut := run(t, "", "--dir", snapDir(t), "check")
	if code != 1 || !strings.Contains(errOut, "no snapshots") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestCheckUnknownNameIsHardError(t *testing.T) {
	code, _, errOut := run(t, "", "--dir", snapDir(t), "check", "ghost")
	if code != 2 || !strings.Contains(errOut, `"ghost"`) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestCheckOneFailureAmongManyStillExitsNonZero(t *testing.T) {
	dir := snapDir(t)
	stateFile := filepath.Join(t.TempDir(), "state.txt")
	os.WriteFile(stateFile, []byte("v1\n"), 0o644)
	run(t, "", "--dir", dir, "record", "stable", "--", "sh", "-c", "echo ok")
	run(t, "", "--dir", dir, "record", "moving", "--", "cat", stateFile)
	os.WriteFile(stateFile, []byte("v2\n"), 0o644)
	code, out, _ := run(t, "", "--dir", dir, "check")
	if code != 1 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out, "1 ok, 1 failed") {
		t.Fatalf("summary wrong: %q", out)
	}
}

func TestListShowsSnapshotsAsTable(t *testing.T) {
	dir := snapDir(t)
	// Empty store first: informational, not an error.
	code, out, _ := run(t, "", "--dir", dir, "list")
	if code != 0 || !strings.Contains(out, "no snapshots") {
		t.Fatalf("empty list: code=%d out=%q", code, out)
	}
	run(t, "", "--dir", dir, "record", "greet", "--", "sh", "-c", "echo hello world")
	code, out, _ = run(t, "", "--dir", dir, "list")
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "greet") {
		t.Fatalf("out=%q", out)
	}
	// Words with spaces are shell-quoted so the row is copy-pasteable.
	if !strings.Contains(out, "'echo hello world'") {
		t.Fatalf("command not quoted: %q", out)
	}
}

func TestShowPrintsSnapshotVerbatim(t *testing.T) {
	dir := snapDir(t)
	run(t, "", "--dir", dir, "record", "greet", "--", "sh", "-c", "echo hi")
	code, out, _ := run(t, "", "--dir", dir, "show", "greet")
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "greet.snap"))
	if out != string(raw) {
		t.Fatalf("show output differs from file:\n%q\nvs\n%q", out, raw)
	}
}

func TestRmDeletesMultipleSnapshots(t *testing.T) {
	dir := snapDir(t)
	run(t, "", "--dir", dir, "record", "a", "--", "sh", "-c", "echo a")
	run(t, "", "--dir", dir, "record", "b", "--", "sh", "-c", "echo b")
	code, out, _ := run(t, "", "--dir", dir, "rm", "a", "b")
	if code != 0 || !strings.Contains(out, "removed a") || !strings.Contains(out, "removed b") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.snap")); !os.IsNotExist(err) {
		t.Fatal("a.snap still exists")
	}
}

func TestRedactFilterMode(t *testing.T) {
	// Default rule set over stdin...
	in := "worker pid 4821 started 2026-07-12T16:09:33Z in /tmp/w0rk\n"
	code, out, _ := run(t, in, "--dir", snapDir(t), "redact")
	want := "worker pid <PID> started <TIMESTAMP> in <TMP>\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d got %q, want %q", code, out, want)
	}
	// ...and an explicit narrow selection leaves everything else alone.
	code, out, _ = run(t, "pid 1 at 16:09:33\n", "--dir", snapDir(t),
		"redact", "--redact", "pid")
	if code != 0 || out != "pid <PID> at 16:09:33\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestConfigCustomRuleUsedByRecordAndCheck(t *testing.T) {
	dir := snapDir(t)
	os.MkdirAll(dir, 0o755)
	cfgJSON := `{"rules":[{"name":"counter","pattern":"count=\\d+","replace":"count=<N>"}]}`
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfgJSON), 0o644)

	// $$ makes the counter genuinely different on every run: only the
	// custom rule keeps this snapshot stable.
	script := writeScript(t, `echo "count=$$"`)
	code, _, errOut := run(t, "", "--dir", dir, "record", "counted", "--", script)
	if code != 0 {
		t.Fatalf("record: code=%d stderr=%q", code, errOut)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "counted.snap"))
	if !strings.Contains(string(data), "count=<N>") {
		t.Fatalf("custom rule not applied:\n%s", data)
	}
	code, out, _ := run(t, "", "--dir", dir, "check", "counted")
	if code != 0 || !strings.Contains(out, "ok      counted") {
		t.Fatalf("check: code=%d out=%q", code, out)
	}
}

func TestConfigDefaultRedactListRespected(t *testing.T) {
	dir := snapDir(t)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"redact":["pid"]}`), 0o644)
	run(t, "", "--dir", dir, "record", "narrow", "--", "sh", "-c",
		"echo pid 7 at 16:09:33")
	data, _ := os.ReadFile(filepath.Join(dir, "narrow.snap"))
	// pid redacted, timestamp NOT — the config narrowed the set.
	if !strings.Contains(string(data), "|pid <PID> at 16:09:33") {
		t.Fatalf("config redact list ignored:\n%s", data)
	}
}

func TestBrokenConfigFailsEveryCommandThatReadsIt(t *testing.T) {
	dir := snapDir(t)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"redact": [`), 0o644)
	code, _, errOut := run(t, "", "--dir", dir, "record", "x", "--", "sh", "-c", "echo hi")
	if code != 2 || !strings.Contains(errOut, "config.json") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestSnapshotRemembersItsRedactorSet(t *testing.T) {
	// Recorded with only 'pid': a later check must not start redacting
	// timestamps just because defaults include them — the snapshot's own
	// list wins, forever.
	dir := snapDir(t)
	run(t, "", "--dir", dir, "record", "--redact", "pid", "pinned",
		"--", "sh", "-c", "echo pid 5 done")
	data, _ := os.ReadFile(filepath.Join(dir, "pinned.snap"))
	if !strings.Contains(string(data), "redact: pid\n") {
		t.Fatalf("redactor set not persisted:\n%s", data)
	}
	code, out, _ := run(t, "", "--dir", dir, "check", "pinned")
	if code != 0 || !strings.Contains(out, "ok      pinned") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestFullRecordCheckUpdateLifecycle(t *testing.T) {
	// The complete user journey in one test: record, green check, drift,
	// red check with diff, update, green again, rm.
	dir := snapDir(t)
	stateFile := filepath.Join(t.TempDir(), "greeting.txt")
	os.WriteFile(stateFile, []byte("hello v1\n"), 0o644)

	if code, _, e := run(t, "", "--dir", dir, "record", "cycle", "--", "cat", stateFile); code != 0 {
		t.Fatalf("record: %s", e)
	}
	if code, _, _ := run(t, "", "--dir", dir, "check"); code != 0 {
		t.Fatal("fresh check failed")
	}
	os.WriteFile(stateFile, []byte("hello v2\n"), 0o644)
	code, out, _ := run(t, "", "--dir", dir, "check")
	if code != 1 || !strings.Contains(out, "-hello v1") || !strings.Contains(out, "+hello v2") {
		t.Fatalf("drift not reported: code=%d out=%q", code, out)
	}
	if code, _, _ := run(t, "", "--dir", dir, "check", "--update"); code != 0 {
		t.Fatal("update failed")
	}
	if code, _, _ := run(t, "", "--dir", dir, "check"); code != 0 {
		t.Fatal("post-update check failed")
	}
	if code, _, _ := run(t, "", "--dir", dir, "rm", "cycle"); code != 0 {
		t.Fatal("rm failed")
	}
}
