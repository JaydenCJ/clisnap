// Package redact implements the volatility-redaction engine.
//
// A snapshot of CLI output is only useful if it is stable across re-runs,
// machines, and days of the week. Real command output rarely is: it embeds
// timestamps, PIDs, temp-dir paths, home directories, heap addresses and
// wall-clock durations. redact rewrites each of those volatile spans to a
// fixed token (for example "<TIMESTAMP>") so that the *shape* of the output
// is captured while the noise is not.
//
// Design rules the whole package obeys:
//
//   - Deterministic: the same input always yields the same output.
//   - Idempotent: Apply(Apply(x)) == Apply(x). Replacement tokens contain
//     no digits and no path separators, so no rule can re-match one.
//   - Ordered: rules run in one canonical order (see Order) regardless of
//     how the caller listed them, so snapshots never depend on flag order.
//   - Conservative: patterns that would over-match plausible stable output
//     (bare dates, epoch integers) exist but are opt-in, not defaults.
package redact

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Rule rewrites every match of Pattern to Replace. Replace may use Go
// regexp expansion syntax ($1, ${name}); literal dollars must be written $$.
type Rule struct {
	Name    string
	Pattern *regexp.Regexp
	Replace string
}

// CustomRule is the user-facing rule spec as it appears in config.json.
// It is compiled into one Rule by Build.
type CustomRule struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
	Replace string `json:"replace"`
}

// Stats counts substitutions per rule name for one Apply call.
// Only rules that matched at least once appear.
type Stats map[string]int

// Total returns the sum of all per-rule counts.
func (s Stats) Total() int {
	n := 0
	for _, c := range s {
		n += c
	}
	return n
}

// Engine applies an ordered list of rules to text.
type Engine struct {
	rules []Rule
}

// Order is the canonical application order for built-in rules.
// ANSI stripping runs first so later patterns see plain text; path rules
// run before token rules so a UUID or epoch inside a temp path is
// swallowed by the path token rather than split in two.
var Order = []string{
	"ansi", "tmp-path", "home-path", "timestamp", "date", "epoch",
	"uuid", "hex-addr", "duration", "pid",
}

// defaultNames is the subset of Order enabled when the caller does not
// choose. "date" and "epoch" are deliberately excluded: a bare date or a
// ten-digit integer is too often stable, meaningful output.
var defaultNames = []string{
	"ansi", "tmp-path", "home-path", "timestamp",
	"uuid", "hex-addr", "duration", "pid",
}

// DefaultNames returns the built-in rules enabled by default, in canonical
// order. Callers receive a copy and may mutate it freely.
func DefaultNames() []string {
	return append([]string(nil), defaultNames...)
}

// BuiltinNames returns every built-in rule name, in canonical order.
func BuiltinNames() []string {
	return append([]string(nil), Order...)
}

// customNameRe validates user-supplied rule names: lowercase alphanumeric
// with interior hyphens, so names survive the comma-separated snapshot
// header and the --redact flag unescaped.
var customNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidCustomName reports whether name is acceptable for a custom rule.
func ValidCustomName(name string) bool {
	return customNameRe.MatchString(name)
}

// builtin holds the compiled built-in rule bodies, keyed by name.
// A single name may expand to several regexes applied in sequence.
var builtin = map[string][]Rule{
	"ansi": {
		// CSI sequences: colors, cursor movement, erase. Replaced with
		// nothing rather than a token — styling is presentation, not data.
		rule("ansi", `\x1b\[[0-9;?]*[ -/]*[@-~]`, ""),
		// OSC sequences (terminal title etc.), BEL- or ST-terminated.
		rule("ansi", `\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`, ""),
	},
	"tmp-path": {
		// The random suffix IS the volatile part, so the whole path
		// collapses to one token. Covers Linux /tmp, macOS /private/tmp
		// and /var/folders, and /dev/shm.
		rule("tmp-path", `(?:/private)?/tmp/[A-Za-z0-9._%+/-]+`, "<TMP>"),
		rule("tmp-path", `/var/folders/[A-Za-z0-9._%+/-]+`, "<TMP>"),
		rule("tmp-path", `/dev/shm/[A-Za-z0-9._%+/-]+`, "<TMP>"),
	},
	"home-path": {
		// Unlike tmp paths, only the user segment is volatile — the tail
		// (project-relative path) is meaningful and is preserved:
		// /home/alice/proj/main.go -> <HOME>/proj/main.go.
		// The current user's real home directory (any OS layout) is added
		// dynamically in BuildWithHome.
		rule("home-path", `/home/[A-Za-z0-9._-]+`, "<HOME>"),
		rule("home-path", `/root\b`, "<HOME>"),
	},
	"timestamp": {
		// RFC 3339 / ISO 8601, with optional fraction and zone.
		rule("timestamp",
			`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`,
			"<TIMESTAMP>"),
		// RFC 1123 as emitted in HTTP Date headers and access logs.
		rule("timestamp",
			`(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun), \d{2} `+monthAlt+` \d{4} \d{2}:\d{2}:\d{2}(?: (?:GMT|UTC|[+-]\d{4}))?`,
			"<TIMESTAMP>"),
		// syslog style: "Jul 12 16:09:33" (day may be space-padded).
		rule("timestamp", monthAlt+` {1,2}\d{1,2} \d{2}:\d{2}:\d{2}`, "<TIMESTAMP>"),
		// Bare clock time. Runs after the full forms above, so it only
		// catches times that were not part of a larger timestamp.
		rule("timestamp", `\b\d{2}:\d{2}:\d{2}(?:\.\d+)?\b`, "<TIMESTAMP>"),
	},
	"date": {
		// Opt-in: a bare date is often stable output (release dates,
		// CHANGELOG lines), so it is not redacted by default.
		rule("date", `\b\d{4}-\d{2}-\d{2}\b`, "<DATE>"),
	},
	"epoch": {
		// Opt-in: 13-digit millisecond epochs first, then 10-digit second
		// epochs, both constrained to the 2017–2033 range so ordinary
		// large integers survive.
		rule("epoch", `\b1[5-9]\d{11}\b`, "<EPOCH>"),
		rule("epoch", `\b1[5-9]\d{8}\b`, "<EPOCH>"),
	},
	"uuid": {
		rule("uuid",
			`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`,
			"<UUID>"),
	},
	"hex-addr": {
		// Pointer-sized 0x literals. The 4-char minimum keeps small
		// constants like 0xFF intact.
		rule("hex-addr", `\b0[xX][0-9A-Fa-f]{4,16}\b`, "<ADDR>"),
	},
	"duration": {
		// Go-style durations: "12ms", "1.5s", "1h2m3.5s". The leading
		// guard group refuses '.', '-' and word characters so version
		// strings like "v1.2s" or "1.2.3s" are never split. RE2 has no
		// lookbehind, so the guard is captured and re-emitted via $1.
		rule("duration",
			`(?m)(^|[^\w.-])((?:\d+h)?(?:\d+m)?\d+(?:\.\d+)?(?:ns|µs|us|ms|s))\b`,
			"${1}<DURATION>"),
	},
	"pid": {
		// "pid 123", "PID: 4", "pid=77" — the label is kept, the number
		// is replaced.
		rule("pid", `\b([Pp][Ii][Dd])([:=#]?\s*)(\d+)`, "${1}${2}<PID>"),
		// syslog process tags: "cron[1234]:". The trailing colon is
		// required so array indexing like "a[10]" is left alone.
		rule("pid", `([A-Za-z][\w.-]*)\[(\d{2,8})\]:`, "${1}[<PID>]:"),
	},
}

const monthAlt = `(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)`

func rule(name, pattern, replace string) Rule {
	return Rule{Name: name, Pattern: regexp.MustCompile(pattern), Replace: replace}
}

// Build compiles an engine from a list of active rule names plus the custom
// rule definitions available to resolve them. The current user's home
// directory is detected automatically; tests use BuildWithHome instead.
func Build(names []string, custom []CustomRule) (*Engine, error) {
	home, _ := os.UserHomeDir()
	return BuildWithHome(names, custom, home)
}

// BuildWithHome is Build with an explicit home directory (empty disables
// the dynamic current-user rule). Custom rules always run before built-ins
// so users can protect or pre-transform spans the defaults would touch;
// built-ins then run in canonical Order regardless of input order.
func BuildWithHome(names []string, custom []CustomRule, home string) (*Engine, error) {
	customByName := make(map[string]Rule, len(custom))
	customOrder := make([]string, 0, len(custom))
	for _, c := range custom {
		if !ValidCustomName(c.Name) {
			return nil, fmt.Errorf("invalid custom rule name %q (want lowercase [a-z0-9-], starting alphanumeric)", c.Name)
		}
		if _, ok := builtin[c.Name]; ok {
			return nil, fmt.Errorf("custom rule %q shadows a built-in rule", c.Name)
		}
		if _, ok := customByName[c.Name]; ok {
			return nil, fmt.Errorf("duplicate custom rule name %q", c.Name)
		}
		re, err := regexp.Compile(c.Pattern)
		if err != nil {
			return nil, fmt.Errorf("custom rule %q: %v", c.Name, err)
		}
		customByName[c.Name] = Rule{Name: c.Name, Pattern: re, Replace: c.Replace}
		customOrder = append(customOrder, c.Name)
	}

	active := make(map[string]bool, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := builtin[n]; !ok {
			if _, ok := customByName[n]; !ok {
				return nil, fmt.Errorf("unknown redactor %q (built-ins: %s)", n, strings.Join(BuiltinNames(), ", "))
			}
		}
		active[n] = true
	}

	var rules []Rule
	for _, n := range customOrder { // customs first, in config order
		if active[n] {
			rules = append(rules, customByName[n])
		}
	}
	for _, n := range Order { // then built-ins, in canonical order
		if !active[n] {
			continue
		}
		if n == "home-path" && home != "" && len(home) > 3 && home != "/root" {
			// Redact the machine's actual home dir literally, whatever
			// the OS layout. Longest-prefix first, before the generic
			// /home rule, keeping the tail: <HOME>/rest/of/path.
			rules = append(rules, Rule{
				Name:    "home-path",
				Pattern: regexp.MustCompile(regexp.QuoteMeta(strings.TrimRight(home, "/"))),
				Replace: "<HOME>",
			})
		}
		rules = append(rules, builtin[n]...)
	}
	return &Engine{rules: rules}, nil
}

// Apply rewrites every volatile span in s and reports how many
// substitutions each rule made.
func (e *Engine) Apply(s string) (string, Stats) {
	stats := Stats{}
	for _, r := range e.rules {
		if n := len(r.Pattern.FindAllStringIndex(s, -1)); n > 0 {
			stats[r.Name] += n
			s = r.Pattern.ReplaceAllString(s, r.Replace)
		}
	}
	return s, stats
}

// RuleNames returns the distinct names of active rules in application
// order, mainly for diagnostics and tests.
func (e *Engine) RuleNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range e.rules {
		if !seen[r.Name] {
			seen[r.Name] = true
			out = append(out, r.Name)
		}
	}
	return out
}

// Normalize canonicalizes a user-supplied rule-name list: trims blanks,
// drops empties, removes duplicates while preserving first-seen order.
// It does not validate names; Build does.
func Normalize(names []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// SortedStats renders stats as "name×count" pairs sorted by name, for
// stable human-readable summaries.
func SortedStats(s Stats) string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s×%d", k, s[k]))
	}
	return strings.Join(parts, " ")
}
