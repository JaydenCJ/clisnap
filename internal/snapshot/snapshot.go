// Package snapshot defines the on-disk snapshot format and the store that
// manages a directory of snapshots.
//
// The format (documented in docs/snapshot-format.md) is a versioned,
// line-oriented text file designed to be reviewed and diffed in code
// review like any other test fixture:
//
//	clisnap snapshot v1
//	cmd: ["sh","-c","./greet.sh"]
//	exit: 0
//	redact: ansi,tmp-path,home-path,timestamp,uuid,hex-addr,duration,pid
//	--- stdout: 2 lines ---
//	|hello
//	|now: <TIMESTAMP>
//	--- stderr: 0 lines ---
//
// Every content line is prefixed with '|', which makes leading/trailing
// whitespace visible and lets output contain anything — including lines
// that look like section headers — without escaping. A missing final
// newline is recorded in the section header so byte-exact round-trips are
// guaranteed.
package snapshot

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Magic is the first line of every v1 snapshot file.
const Magic = "clisnap snapshot v1"

// Snapshot is one recorded command execution after redaction.
type Snapshot struct {
	// Cmd is the exact argv that was (and will be re-)executed.
	Cmd []string
	// Exit is the recorded exit code; it is part of the assertion.
	Exit int
	// Redactors lists the rule names that were active at record time.
	// check re-applies exactly this set, so later changes to the default
	// rule set never silently alter an existing snapshot's meaning.
	Redactors []string
	// Stdout and Stderr hold the redacted output, byte-exact.
	Stdout string
	Stderr string
}

// Encode renders s in the v1 text format. It never fails for snapshots
// produced by this program; the error covers impossible inputs (a nil
// command) so corruption cannot be written silently.
func Encode(s *Snapshot) ([]byte, error) {
	if len(s.Cmd) == 0 {
		return nil, fmt.Errorf("snapshot has empty command")
	}
	cmdJSON, err := json.Marshal(s.Cmd)
	if err != nil {
		return nil, fmt.Errorf("encode command: %v", err)
	}
	var b strings.Builder
	b.WriteString(Magic + "\n")
	b.WriteString("cmd: " + string(cmdJSON) + "\n")
	b.WriteString("exit: " + strconv.Itoa(s.Exit) + "\n")
	b.WriteString("redact: " + encodeRedactors(s.Redactors) + "\n")
	writeSection(&b, "stdout", s.Stdout)
	writeSection(&b, "stderr", s.Stderr)
	return []byte(b.String()), nil
}

func encodeRedactors(names []string) string {
	if len(names) == 0 {
		return "-" // explicit marker: recorded with redaction disabled
	}
	return strings.Join(names, ",")
}

func writeSection(b *strings.Builder, name, text string) {
	lines, noNL := SplitLines(text)
	suffix := ""
	if noNL {
		suffix = " (no trailing newline)"
	}
	fmt.Fprintf(b, "--- %s: %d lines%s ---\n", name, len(lines), suffix)
	for _, l := range lines {
		b.WriteString("|")
		b.WriteString(l)
		b.WriteString("\n")
	}
}

// SplitLines splits text into lines without terminators and reports
// whether the final line lacked a trailing newline. "" yields (nil,
// false); "a\n\n" yields (["a", ""], false); "a" yields (["a"], true).
func SplitLines(text string) ([]string, bool) {
	if text == "" {
		return nil, false
	}
	noNL := !strings.HasSuffix(text, "\n")
	trimmed := strings.TrimSuffix(text, "\n")
	return strings.Split(trimmed, "\n"), noNL
}

// JoinLines is the inverse of SplitLines.
func JoinLines(lines []string, noNL bool) string {
	if len(lines) == 0 {
		return ""
	}
	s := strings.Join(lines, "\n")
	if !noNL {
		s += "\n"
	}
	return s
}

var sectionRe = regexp.MustCompile(`^--- (stdout|stderr): (\d+) lines( \(no trailing newline\))? ---$`)

// Decode parses a v1 snapshot file. It is strict: every deviation from
// the format is an error with a 1-based line number, so a hand-edited or
// merge-mangled snapshot fails loudly instead of comparing garbage.
func Decode(data []byte) (*Snapshot, error) {
	// Split into physical lines; the file must end with a newline.
	raw := string(data)
	if raw == "" {
		return nil, fmt.Errorf("empty snapshot file")
	}
	if !strings.HasSuffix(raw, "\n") {
		return nil, fmt.Errorf("snapshot file is truncated (missing final newline)")
	}
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")

	p := &parser{lines: lines}
	if got := p.next(); got != Magic {
		return nil, p.errf("expected header %q, got %q", Magic, got)
	}

	s := &Snapshot{}
	cmdLine, err := p.field("cmd")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(cmdLine), &s.Cmd); err != nil {
		return nil, p.errf("bad cmd JSON: %v", err)
	}
	if len(s.Cmd) == 0 {
		return nil, p.errf("cmd must be a non-empty JSON string array")
	}

	exitLine, err := p.field("exit")
	if err != nil {
		return nil, err
	}
	s.Exit, err = strconv.Atoi(exitLine)
	if err != nil {
		return nil, p.errf("bad exit code %q", exitLine)
	}

	redactLine, err := p.field("redact")
	if err != nil {
		return nil, err
	}
	if redactLine != "-" {
		s.Redactors = strings.Split(redactLine, ",")
	}

	if s.Stdout, err = p.section("stdout"); err != nil {
		return nil, err
	}
	if s.Stderr, err = p.section("stderr"); err != nil {
		return nil, err
	}
	if !p.done() {
		return nil, p.errf("unexpected trailing content %q", p.peek())
	}
	return s, nil
}

// parser is a line cursor with positioned errors.
type parser struct {
	lines []string
	pos   int
}

func (p *parser) done() bool { return p.pos >= len(p.lines) }

func (p *parser) peek() string {
	if p.done() {
		return ""
	}
	return p.lines[p.pos]
}

func (p *parser) next() string {
	l := p.peek()
	p.pos++
	return l
}

func (p *parser) errf(format string, args ...any) error {
	return fmt.Errorf("snapshot line %d: %s", p.pos, fmt.Sprintf(format, args...))
}

// field consumes a "key: value" line and returns the value.
func (p *parser) field(key string) (string, error) {
	if p.done() {
		return "", p.errf("unexpected end of file, expected %q field", key)
	}
	l := p.next()
	prefix := key + ": "
	if !strings.HasPrefix(l, prefix) {
		return "", p.errf("expected %q field, got %q", key, l)
	}
	return strings.TrimPrefix(l, prefix), nil
}

// section consumes a "--- name: N lines ---" header plus N '|' lines and
// returns the reconstructed text.
func (p *parser) section(name string) (string, error) {
	if p.done() {
		return "", p.errf("unexpected end of file, expected %s section", name)
	}
	header := p.next()
	m := sectionRe.FindStringSubmatch(header)
	if m == nil || m[1] != name {
		return "", p.errf("expected %s section header, got %q", name, header)
	}
	count, _ := strconv.Atoi(m[2])
	noNL := m[3] != ""
	if count == 0 && noNL {
		return "", p.errf("empty %s section cannot lack a trailing newline", name)
	}
	lines := make([]string, 0, count)
	for i := 0; i < count; i++ {
		if p.done() {
			return "", p.errf("%s section promises %d lines but file ends after %d", name, count, i)
		}
		l := p.next()
		if !strings.HasPrefix(l, "|") {
			return "", p.errf("%s content line missing '|' prefix: %q", name, l)
		}
		lines = append(lines, l[1:])
	}
	return JoinLines(lines, noNL), nil
}
