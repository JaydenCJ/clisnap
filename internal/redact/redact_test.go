// Tests for the volatility-redaction engine. Every case documents why it
// matters: false negatives make snapshots churn, false positives silently
// erase real assertions, and both are bugs.
package redact

import (
	"reflect"
	"strings"
	"testing"
)

// build is a test helper: engine over the given names with the dynamic
// home rule pinned to a fixed fake home for determinism.
func build(t *testing.T, names []string, custom ...CustomRule) *Engine {
	t.Helper()
	e, err := BuildWithHome(names, custom, "/srv/agents/build-7")
	if err != nil {
		t.Fatalf("BuildWithHome(%v): %v", names, err)
	}
	return e
}

func apply(t *testing.T, names []string, in string) string {
	t.Helper()
	out, _ := build(t, names).Apply(in)
	return out
}

func TestTimestampFormatsRedacted(t *testing.T) {
	cases := []struct{ desc, in, want string }{
		{"RFC 3339 UTC",
			"started at 2026-07-12T16:09:33Z ok", "started at <TIMESTAMP> ok"},
		{"RFC 3339 with fraction and offset",
			"t=2026-07-12T16:09:33.123456+09:00", "t=<TIMESTAMP>"},
		{"space-separated datetime, the common log-framework shape",
			"[2026-07-12 16:09:33] request handled", "[<TIMESTAMP>] request handled"},
		{"RFC 1123 as in HTTP Date headers",
			"Date: Sun, 12 Jul 2026 16:09:33 GMT", "Date: <TIMESTAMP>"},
		{"syslog with space-padded single-digit day",
			"Jul  2 06:01:02 host booted", "<TIMESTAMP> host booted"},
		{"bare clock time",
			"finished 16:09:33 sharp", "finished <TIMESTAMP> sharp"},
	}
	for _, c := range cases {
		if got := apply(t, []string{"timestamp"}, c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.desc, got, c.want)
		}
	}
}

func TestOptInRulesAreOffByDefault(t *testing.T) {
	// A bare date is often stable output (release dates, CHANGELOG
	// lines) and ten-digit integers are frequently row counts or byte
	// sizes — redacting them by default would erase real assertions.
	for _, in := range []string{"released on 2025-01-15", "total 1783701234 bytes"} {
		if got := apply(t, DefaultNames(), in); got != in {
			t.Errorf("default rules mangled stable output: %q -> %q", in, got)
		}
	}
}

func TestOptInRulesRedactWhenSelected(t *testing.T) {
	cases := []struct{ rule, in, want string }{
		{"date", "released on 2025-01-15", "released on <DATE>"},
		{"epoch", "mtime=1783701234", "mtime=<EPOCH>"},
		{"epoch", "ts 1783701234567 end", "ts <EPOCH> end"}, // millisecond form
	}
	for _, c := range cases {
		if got := apply(t, []string{c.rule}, c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.rule, got, c.want)
		}
	}
}

func TestDurationRedaction(t *testing.T) {
	cases := []struct{ desc, in, want string }{
		{"sub-second units",
			"compile 812ms link 93us total 12ns",
			"compile <DURATION> link <DURATION> total <DURATION>"},
		{"compound Go-style duration",
			"elapsed 1h2m3.5s (wall)", "elapsed <DURATION> (wall)"},
		{"start of line counts as a boundary",
			"1.5s to boot\n2s to serve\n", "<DURATION> to boot\n<DURATION> to serve\n"},
		// "v1.2s" and "1.2.3s" look duration-like but are identifiers;
		// the leading guard refuses word characters and dots.
		{"version strings survive",
			"tool v1.2s build 1.2.3s", "tool v1.2s build 1.2.3s"},
	}
	for _, c := range cases {
		if got := apply(t, []string{"duration"}, c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.desc, got, c.want)
		}
	}
}

func TestPidRedaction(t *testing.T) {
	cases := []struct{ desc, in, want string }{
		{"labelled pids keep the label",
			"pid 4821 / PID: 3 / pid=99181", "pid <PID> / PID: <PID> / pid=<PID>"},
		{"syslog process tag",
			"cron[28461]: job started", "cron[<PID>]: job started"},
		// Bracketed digits without the trailing colon are program output
		// (array indexing), and "rapid 5" must not look like "pid 5".
		{"non-pid lookalikes survive",
			"a[10] = 7 and rapid 5", "a[10] = 7 and rapid 5"},
	}
	for _, c := range cases {
		if got := apply(t, []string{"pid"}, c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.desc, got, c.want)
		}
	}
}

func TestTmpPathsRedactedWhole(t *testing.T) {
	// The random suffix is the volatile part, so the entire path
	// collapses — unlike home paths, where the tail is preserved.
	in := "work in /tmp/clisnap-x1B9 and /var/folders/ab/T/x and /private/tmp/z.1"
	got := apply(t, []string{"tmp-path"}, in)
	want := "work in <TMP> and <TMP> and <TMP>"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestHomePathRedaction(t *testing.T) {
	cases := []struct{ desc, in, want string }{
		{"generic home keeps the project tail",
			"src at /home/alice/proj/main.go", "src at <HOME>/proj/main.go"},
		{"root home (containers) is a home too",
			"cache: /root/.cache/tool", "cache: <HOME>/.cache/tool"},
		// The dynamic rule redacts the machine's real home dir even when
		// it matches no generic pattern (CI runners, service accounts).
		{"current user's home, whatever the layout",
			"config: /srv/agents/build-7/.toolrc", "config: <HOME>/.toolrc"},
	}
	for _, c := range cases {
		if got := apply(t, []string{"home-path"}, c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.desc, got, c.want)
		}
	}
}

func TestHexAddressAndUUIDRedaction(t *testing.T) {
	cases := []struct {
		rule, in, want string
	}{
		{"hex-addr", "goroutine at 0xc000123abc waiting", "goroutine at <ADDR> waiting"},
		// 0xFF is a value, not an address; the 4-digit minimum protects it.
		{"hex-addr", "mask 0xFF set", "mask 0xFF set"},
		{"uuid", "request 6ba7b810-9dad-11d1-80b4-00c04fd430c8 done", "request <UUID> done"},
	}
	for _, c := range cases {
		if got := apply(t, []string{c.rule}, c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.rule, got, c.want)
		}
	}
}

func TestAnsiEscapesStripped(t *testing.T) {
	in := "\x1b[1;32mPASS\x1b[0m all good\x1b]0;title\x07"
	got := apply(t, []string{"ansi"}, in)
	if got != "PASS all good" {
		t.Fatalf("got %q", got)
	}
}

func TestUUIDInsideTmpPathSwallowedByPathRule(t *testing.T) {
	// Canonical ordering: path rules run before token rules, so a
	// UUID-suffixed temp dir yields one <TMP> token, not a token split
	// in two ("<TMP>...<UUID>/out.txt" would churn on every layout tweak).
	got := apply(t, DefaultNames(), "log: /tmp/run-6ba7b810-9dad-11d1-80b4-00c04fd430c8/out.txt")
	if got != "log: <TMP>" {
		t.Fatalf("got %q", got)
	}
}

func TestRuleOrderIndependentOfInputOrder(t *testing.T) {
	in := "pid 12 at 2026-07-12T16:09:33Z in /tmp/x9"
	a := apply(t, []string{"pid", "timestamp", "tmp-path"}, in)
	b := apply(t, []string{"tmp-path", "timestamp", "pid"}, in)
	if a != b {
		t.Fatalf("order-dependent results: %q vs %q", a, b)
	}
}

func TestCustomRuleAppliedWithGroupExpansion(t *testing.T) {
	e := build(t, []string{"port"},
		CustomRule{Name: "port", Pattern: `(127\.0\.0\.1):\d+`, Replace: "${1}:<PORT>"})
	got, stats := e.Apply("listening on 127.0.0.1:49213")
	if got != "listening on 127.0.0.1:<PORT>" {
		t.Fatalf("got %q", got)
	}
	if stats["port"] != 1 {
		t.Fatalf("stats = %v, want port×1", stats)
	}
}

func TestCustomRuleRunsBeforeBuiltins(t *testing.T) {
	// A custom rule can claim a span the built-ins would otherwise eat;
	// here the whole bracketed timestamp becomes one project token.
	e := build(t, []string{"stamp", "timestamp"},
		CustomRule{Name: "stamp", Pattern: `\[\d{4}-\d{2}-\d{2}T[0-9:.Z]+\]`, Replace: "<STAMP>"})
	got, _ := e.Apply("[2026-07-12T16:09:33Z] boot")
	if got != "<STAMP> boot" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildRejectsBadInput(t *testing.T) {
	cases := []struct {
		desc    string
		names   []string
		custom  []CustomRule
		wantErr string
	}{
		{"unknown redactor name",
			[]string{"no-such-rule"}, nil, "no-such-rule"},
		{"uncompilable custom pattern",
			[]string{"bad"}, []CustomRule{{Name: "bad", Pattern: `([`, Replace: "x"}}, "bad"},
		{"custom rule shadowing a built-in",
			[]string{"pid"}, []CustomRule{{Name: "pid", Pattern: `x`, Replace: "y"}}, "shadows"},
		{"duplicate custom rule names",
			nil, []CustomRule{
				{Name: "dup", Pattern: `a`, Replace: "b"},
				{Name: "dup", Pattern: `c`, Replace: "d"},
			}, "duplicate"},
		{"empty custom rule name", nil, []CustomRule{{Name: "", Pattern: `a`}}, "invalid"},
		{"uppercase custom rule name", nil, []CustomRule{{Name: "UPPER", Pattern: `a`}}, "invalid"},
		{"custom rule name with slash", nil, []CustomRule{{Name: "a/b", Pattern: `a`}}, "invalid"},
	}
	for _, c := range cases {
		_, err := BuildWithHome(c.names, c.custom, "")
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want mention of %q", c.desc, err, c.wantErr)
		}
	}
}

func TestRedactionIsIdempotent(t *testing.T) {
	// Apply(Apply(x)) == Apply(x) is what makes 'check' sound: fresh
	// output and recorded output pass through the same engine, and a
	// token must never be re-mangled. Exercise every default rule.
	corpus := "\x1b[31mE\x1b[0m 2026-07-12T16:09:33Z pid 42 in /tmp/a1 " +
		"/home/bob/x 0xdeadbeef 6ba7b810-9dad-11d1-80b4-00c04fd430c8 took 1.5s"
	e := build(t, DefaultNames())
	once, _ := e.Apply(corpus)
	twice, _ := e.Apply(once)
	if once != twice {
		t.Fatalf("not idempotent:\n once=%q\ntwice=%q", once, twice)
	}
}

func TestStatsCountPerRule(t *testing.T) {
	e := build(t, DefaultNames())
	_, stats := e.Apply("pid 1 pid 2 at 2026-07-12T16:09:33Z")
	if stats["pid"] != 2 || stats["timestamp"] != 1 {
		t.Fatalf("stats = %v, want pid×2 timestamp×1", stats)
	}
	if stats.Total() != 3 {
		t.Fatalf("Total() = %d, want 3", stats.Total())
	}
}

func TestEmptyEngineLeavesInputUntouched(t *testing.T) {
	e := build(t, nil)
	in := "pid 42 at 2026-07-12T16:09:33Z"
	got, stats := e.Apply(in)
	if got != in || len(stats) != 0 {
		t.Fatalf("got %q stats %v, want passthrough", got, stats)
	}
}

func TestDefaultNamesExcludeOptInsAndReturnCopy(t *testing.T) {
	a := DefaultNames()
	for _, n := range a {
		if n == "date" || n == "epoch" {
			t.Fatalf("opt-in rule %q leaked into defaults", n)
		}
	}
	a[0] = "mutated"
	if b := DefaultNames(); b[0] == "mutated" {
		t.Fatal("DefaultNames exposes internal slice")
	}
}

func TestNormalizeTrimsAndDeduplicates(t *testing.T) {
	got := Normalize([]string{" pid", "", "pid", "uuid ", "pid"})
	if !reflect.DeepEqual(got, []string{"pid", "uuid"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSortedStatsIsStable(t *testing.T) {
	s := Stats{"uuid": 2, "pid": 1, "ansi": 3}
	if got := SortedStats(s); got != "ansi×3 pid×1 uuid×2" {
		t.Fatalf("got %q", got)
	}
}

func TestRuleNamesReflectApplicationOrder(t *testing.T) {
	e := build(t, []string{"pid", "ansi", "custom-x"},
		CustomRule{Name: "custom-x", Pattern: `zzz`, Replace: "<X>"})
	got := e.RuleNames()
	want := []string{"custom-x", "ansi", "pid"} // customs first, then canonical order
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
