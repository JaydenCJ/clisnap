// Tests for the v1 snapshot text format. Round-trip fidelity is the core
// contract: check compares recorded bytes against fresh bytes, so any
// encode/decode loss would surface as phantom test failures.
package snapshot

import (
	"reflect"
	"strings"
	"testing"
)

func sample() *Snapshot {
	return &Snapshot{
		Cmd:       []string{"sh", "-c", "echo hi"},
		Exit:      0,
		Redactors: []string{"timestamp", "pid"},
		Stdout:    "hi\n",
		Stderr:    "",
	}
}

func roundTrip(t *testing.T, s *Snapshot) *Snapshot {
	t.Helper()
	data, err := Encode(s)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v\nfile:\n%s", err, data)
	}
	return got
}

func TestEncodeDecodeRoundTripBasic(t *testing.T) {
	s := sample()
	if got := roundTrip(t, s); !reflect.DeepEqual(got, s) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, s)
	}
}

func TestRoundTripPreservesTrickyContent(t *testing.T) {
	cases := []struct {
		desc   string
		mutate func(*Snapshot)
	}{
		{"empty streams", func(s *Snapshot) { s.Stdout, s.Stderr = "", "" }},
		// printf-style CLIs often omit the final newline; losing that
		// fact would hide a real behavior change.
		{"missing trailing newline", func(s *Snapshot) { s.Stdout = "prompt> " }},
		{"trailing blank lines", func(s *Snapshot) { s.Stdout = "a\n\n\n" }},
		// Output may itself contain '|' prefixes or lines that look
		// exactly like section headers; framing must make them inert.
		{"pipe and header lookalikes", func(s *Snapshot) {
			s.Stdout = "|already piped\n--- stdout: 9 lines ---\n\\ tricky\n"
		}},
		// CRLF producers and progress bars emit \r; it is content.
		{"carriage returns", func(s *Snapshot) { s.Stderr = "50%\rdone\r\n" }},
		{"empty redactor list", func(s *Snapshot) { s.Redactors = nil }},
		{"non-zero exit code", func(s *Snapshot) { s.Exit = 3 }},
	}
	for _, c := range cases {
		s := sample()
		c.mutate(s)
		if got := roundTrip(t, s); !reflect.DeepEqual(got, s) {
			t.Errorf("%s: round trip mismatch:\n got %+v\nwant %+v", c.desc, got, s)
		}
	}
}

func TestEncodeExactLayout(t *testing.T) {
	// The format is a public contract (docs/snapshot-format.md); pin it
	// byte-for-byte so accidental drift is caught here, not in user diffs.
	s := &Snapshot{
		Cmd:       []string{"./greet.sh"},
		Exit:      0,
		Redactors: []string{"timestamp", "pid"},
		Stdout:    "hello\nnow: <TIMESTAMP>\n",
		Stderr:    "warn",
	}
	data, err := Encode(s)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := strings.Join([]string{
		"clisnap snapshot v1",
		`cmd: ["./greet.sh"]`,
		"exit: 0",
		"redact: timestamp,pid",
		"--- stdout: 2 lines ---",
		"|hello",
		"|now: <TIMESTAMP>",
		"--- stderr: 1 lines (no trailing newline) ---",
		"|warn",
		"",
	}, "\n")
	if string(data) != want {
		t.Fatalf("layout drift:\n got:\n%s\nwant:\n%s", data, want)
	}
}

func TestEncodeRejectsEmptyCommand(t *testing.T) {
	if _, err := Encode(&Snapshot{}); err == nil {
		t.Fatal("empty command accepted")
	}
}

func TestDecodeRejectsMalformedFiles(t *testing.T) {
	// Each case is a realistic corruption: hand edits, merge conflicts,
	// truncated writes. All must fail loudly with a positioned error
	// instead of comparing garbage.
	valid, _ := Encode(sample())
	cases := []struct {
		desc    string
		mutate  func(string) string
		wantErr string
	}{
		{"empty file",
			func(string) string { return "" }, "empty"},
		{"wrong magic version",
			func(f string) string { return strings.Replace(f, "v1", "v9", 1) }, "line 1"},
		{"missing final newline (truncated write)",
			func(f string) string { return f[:len(f)-1] }, "truncated"},
		{"overstated line count",
			func(f string) string {
				return strings.Replace(f, "--- stdout: 1 lines ---", "--- stdout: 5 lines ---", 1)
			}, "line 7"},
		{"content line missing '|' prefix",
			func(f string) string { return strings.Replace(f, "|hi", "hi", 1) }, "'|' prefix"},
		{"non-numeric exit code",
			func(f string) string { return strings.Replace(f, "exit: 0", "exit: zero", 1) }, "exit"},
		{"cmd is not JSON",
			func(f string) string {
				return strings.Replace(f, `cmd: ["sh","-c","echo hi"]`, `cmd: not-json`, 1)
			}, "cmd"},
		{"trailing garbage after last section",
			func(f string) string { return f + "extra line\n" }, "trailing"},
		{"contradictory empty no-newline section",
			func(f string) string {
				return strings.Replace(f, "--- stderr: 0 lines ---",
					"--- stderr: 0 lines (no trailing newline) ---", 1)
			}, "trailing newline"},
	}
	for _, c := range cases {
		_, err := Decode([]byte(c.mutate(string(valid))))
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want mention of %q", c.desc, err, c.wantErr)
		}
	}
}

func TestDecodeRejectsCountPastEndOfFile(t *testing.T) {
	file := strings.Join([]string{
		Magic,
		`cmd: ["x"]`,
		"exit: 0",
		"redact: -",
		"--- stdout: 3 lines ---",
		"|only one",
		"",
	}, "\n")
	_, err := Decode([]byte(file))
	if err == nil || !strings.Contains(err.Error(), "promises 3 lines") {
		t.Fatalf("err = %v, want promised-count error", err)
	}
}

func TestSplitLinesCases(t *testing.T) {
	cases := []struct {
		in    string
		lines []string
		noNL  bool
	}{
		{"", nil, false},
		{"\n", []string{""}, false},
		{"a", []string{"a"}, true},
		{"a\n", []string{"a"}, false},
		{"a\nb", []string{"a", "b"}, true},
		{"a\n\n", []string{"a", ""}, false},
	}
	for _, c := range cases {
		lines, noNL := SplitLines(c.in)
		if !reflect.DeepEqual(lines, c.lines) || noNL != c.noNL {
			t.Errorf("SplitLines(%q) = %v,%v want %v,%v", c.in, lines, noNL, c.lines, c.noNL)
		}
		if back := JoinLines(lines, noNL); back != c.in {
			t.Errorf("JoinLines round trip of %q gave %q", c.in, back)
		}
	}
}
