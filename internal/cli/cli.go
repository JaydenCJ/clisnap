// Package cli implements the clisnap command-line interface.
//
// The entry point is Run, which takes argv and explicit streams and
// returns a process exit code. Keeping the CLI a pure function of its
// inputs (no os.Exit, no global state) is what lets the integration tests
// drive every subcommand in-process, deterministically, with no PATH or
// working-directory coupling.
//
// Exit codes:
//
//	0  success — snapshots recorded, or every checked snapshot matched
//	1  mismatch — at least one snapshot check failed (or the store was
//	   empty on a check-all, which would otherwise vacuously "pass")
//	2  usage, configuration or I/O error
package cli

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/JaydenCJ/clisnap/internal/config"
	"github.com/JaydenCJ/clisnap/internal/diff"
	"github.com/JaydenCJ/clisnap/internal/redact"
	"github.com/JaydenCJ/clisnap/internal/runner"
	"github.com/JaydenCJ/clisnap/internal/snapshot"
	"github.com/JaydenCJ/clisnap/internal/version"
)

const (
	exitOK       = 0
	exitMismatch = 1
	exitError    = 2

	defaultDir  = ".clisnap"
	diffContext = 3
)

const usageText = `clisnap %s — snapshot testing for CLIs

Usage:
  clisnap [--dir DIR] <command> [flags] [args]

Commands:
  record [--redact LIST] [--shell] [--force] <name> -- <cmd> [args...]
        run a command, redact volatile output, save it as a snapshot
  check [--update] [name...]
        re-run recorded commands and diff against their snapshots
        (no names = all snapshots; --update rewrites failing ones)
  list  list snapshots with exit codes and commands
  show <name>
        print a snapshot file verbatim
  rm <name...>
        delete snapshots
  redact [--redact LIST]
        filter stdin through the redaction engine to stdout
  version
        print the clisnap version

Global flags (before the command):
  --dir DIR      snapshot directory (default %q)
  --version, -V  print the clisnap version
  --help, -h     show this help

Redactors (default set marked *):
  %s

Exit codes: 0 ok, 1 snapshot mismatch, 2 usage/config/IO error.
`

// Run executes one clisnap invocation and returns its exit code.
func Run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	dir := defaultDir

	i := 0
	for i < len(argv) {
		arg := argv[i]
		switch {
		case arg == "--dir" && i+1 < len(argv):
			dir = argv[i+1]
			i += 2
		case strings.HasPrefix(arg, "--dir="):
			dir = strings.TrimPrefix(arg, "--dir=")
			i++
		case arg == "--version" || arg == "-V":
			fmt.Fprintf(stdout, "clisnap %s\n", version.Version)
			return exitOK
		case arg == "--help" || arg == "-h":
			printUsage(stdout)
			return exitOK
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(stderr, "clisnap: unknown global flag %q\n", arg)
			printUsage(stderr)
			return exitError
		default:
			return dispatch(arg, argv[i+1:], dir, stdin, stdout, stderr)
		}
	}
	printUsage(stderr)
	return exitError
}

func dispatch(cmd string, args []string, dir string, stdin io.Reader, stdout, stderr io.Writer) int {
	switch cmd {
	case "record":
		return cmdRecord(args, dir, stdout, stderr)
	case "check":
		return cmdCheck(args, dir, stdout, stderr)
	case "list":
		return cmdList(args, dir, stdout, stderr)
	case "show":
		return cmdShow(args, dir, stdout, stderr)
	case "rm":
		return cmdRm(args, dir, stdout, stderr)
	case "redact":
		return cmdRedact(args, dir, stdin, stdout, stderr)
	case "version":
		fmt.Fprintf(stdout, "clisnap %s\n", version.Version)
		return exitOK
	case "help":
		printUsage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "clisnap: unknown command %q (see 'clisnap help')\n", cmd)
		return exitError
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, usageText, version.Version, defaultDir, redactorSummary())
}

// redactorSummary renders the built-in redactor names for the help text,
// starring the default set.
func redactorSummary() string {
	defaults := map[string]bool{}
	for _, n := range redact.DefaultNames() {
		defaults[n] = true
	}
	parts := make([]string, 0, len(redact.BuiltinNames()))
	for _, n := range redact.BuiltinNames() {
		if defaults[n] {
			parts = append(parts, n+"*")
		} else {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, " ")
}

func errf(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "clisnap: "+format+"\n", args...)
	return exitError
}

// newFlagSet builds a silent FlagSet; errors are reported by the caller
// so all output flows through the injected streams.
func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// resolveRedactors decides which rule names a recording uses:
//
//	--redact none      -> no redaction at all
//	--redact a,b,c     -> exactly those rules
//	(config "redact")  -> exactly the configured list
//	otherwise          -> built-in defaults plus every custom rule
//
// The chosen list is normalized and stored in the snapshot, so checks
// replay the exact same set forever.
func resolveRedactors(flagVal string, cfg *config.Config) []string {
	if flagVal == "none" {
		return nil
	}
	if flagVal != "" {
		return redact.Normalize(strings.Split(flagVal, ","))
	}
	if len(cfg.Redact) > 0 {
		return redact.Normalize(cfg.Redact)
	}
	return redact.Normalize(append(redact.DefaultNames(), cfg.CustomNames()...))
}

func cmdRecord(args []string, dir string, stdout, stderr io.Writer) int {
	fs := newFlagSet("record", stderr)
	redactFlag := fs.String("redact", "", "comma-separated redactor names, or 'none'")
	shell := fs.Bool("shell", false, "join the command words and run them via 'sh -c'")
	force := fs.Bool("force", false, "overwrite an existing snapshot")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	rest := fs.Args()
	if len(rest) < 3 || rest[1] != "--" {
		return errf(stderr, "usage: clisnap record [flags] <name> -- <cmd> [args...]")
	}
	name, cmd := rest[0], rest[2:]
	if !snapshot.ValidName(name) {
		return errf(stderr, "invalid snapshot name %q (want [A-Za-z0-9][A-Za-z0-9._-]*)", name)
	}
	if *shell {
		cmd = []string{"/bin/sh", "-c", strings.Join(cmd, " ")}
	}

	cfg, err := config.Load(dir)
	if err != nil {
		return errf(stderr, "%v", err)
	}
	names := resolveRedactors(*redactFlag, cfg)
	engine, err := redact.Build(names, cfg.Rules)
	if err != nil {
		return errf(stderr, "%v", err)
	}

	res, err := runner.Run(cmd)
	if err != nil {
		return errf(stderr, "%v", err)
	}
	outText, outStats := engine.Apply(res.Stdout)
	errText, errStats := engine.Apply(res.Stderr)

	snap := &snapshot.Snapshot{
		Cmd:       cmd,
		Exit:      res.Exit,
		Redactors: names,
		Stdout:    outText,
		Stderr:    errText,
	}
	st := snapshot.Store{Dir: dir}
	if err := st.Save(name, snap, *force); err != nil {
		return errf(stderr, "%v", err)
	}
	total := outStats.Total() + errStats.Total()
	fmt.Fprintf(stdout, "recorded %s -> %s (exit %d, %d redactions)\n",
		name, st.Path(name), res.Exit, total)
	if detail := redact.SortedStats(merge(outStats, errStats)); detail != "" {
		fmt.Fprintf(stdout, "  redacted: %s\n", detail)
	}
	return exitOK
}

func merge(a, b redact.Stats) redact.Stats {
	out := redact.Stats{}
	for k, v := range a {
		out[k] += v
	}
	for k, v := range b {
		out[k] += v
	}
	return out
}

func cmdCheck(args []string, dir string, stdout, stderr io.Writer) int {
	fs := newFlagSet("check", stderr)
	update := fs.Bool("update", false, "rewrite snapshots that no longer match")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	st := snapshot.Store{Dir: dir}
	names := fs.Args()
	if len(names) == 0 {
		all, err := st.List()
		if err != nil {
			return errf(stderr, "%v", err)
		}
		if len(all) == 0 {
			// An empty store must not vacuously pass: in CI that would
			// green-light a project whose snapshots were never committed.
			fmt.Fprintf(stderr, "clisnap: no snapshots in %s (record one first)\n", dir)
			return exitMismatch
		}
		names = all
	} else {
		sort.Strings(names)
	}

	cfg, err := config.Load(dir)
	if err != nil {
		return errf(stderr, "%v", err)
	}

	ok, updated, failed := 0, 0, 0
	for _, name := range names {
		status, err := checkOne(st, cfg, name, *update, stdout)
		if err != nil {
			return errf(stderr, "%v", err)
		}
		switch status {
		case "ok":
			ok++
		case "updated":
			updated++
		default:
			failed++
		}
	}

	noun := "snapshots"
	if len(names) == 1 {
		noun = "snapshot"
	}
	summary := fmt.Sprintf("%d %s: %d ok", len(names), noun, ok)
	if updated > 0 {
		summary += fmt.Sprintf(", %d updated", updated)
	}
	if failed > 0 {
		summary += fmt.Sprintf(", %d failed", failed)
	}
	fmt.Fprintln(stdout, summary)
	if failed > 0 {
		return exitMismatch
	}
	return exitOK
}

// checkOne re-runs one snapshot's command and compares. It returns "ok",
// "updated" or "failed"; hard errors (missing snapshot, unresolvable
// rules, unstartable command) abort the whole check instead.
func checkOne(st snapshot.Store, cfg *config.Config, name string, update bool, stdout io.Writer) (string, error) {
	snap, err := st.Load(name)
	if err != nil {
		return "", err
	}
	engine, err := redact.Build(snap.Redactors, cfg.Rules)
	if err != nil {
		return "", fmt.Errorf("snapshot %q: %v (define missing custom rules in %s/%s)",
			name, err, st.Dir, config.FileName)
	}
	res, err := runner.Run(snap.Cmd)
	if err != nil {
		return "", fmt.Errorf("snapshot %q: %v", name, err)
	}
	outText, _ := engine.Apply(res.Stdout)
	errText, _ := engine.Apply(res.Stderr)

	exitOKMatch := res.Exit == snap.Exit
	stdoutDiff := diff.Unified(name+".snap stdout", "current stdout", snap.Stdout, outText, diffContext)
	stderrDiff := diff.Unified(name+".snap stderr", "current stderr", snap.Stderr, errText, diffContext)

	if exitOKMatch && stdoutDiff == "" && stderrDiff == "" {
		fmt.Fprintf(stdout, "ok      %s\n", name)
		return "ok", nil
	}

	if update {
		snap.Exit = res.Exit
		snap.Stdout = outText
		snap.Stderr = errText
		if err := st.Save(name, snap, true); err != nil {
			return "", err
		}
		fmt.Fprintf(stdout, "updated %s\n", name)
		return "updated", nil
	}

	fmt.Fprintf(stdout, "FAIL    %s\n", name)
	if !exitOKMatch {
		fmt.Fprintf(stdout, "  exit code: snapshot %d, current %d\n", snap.Exit, res.Exit)
	}
	for _, d := range []string{stdoutDiff, stderrDiff} {
		if d != "" {
			fmt.Fprint(stdout, d)
		}
	}
	return "failed", nil
}

func cmdList(args []string, dir string, stdout, stderr io.Writer) int {
	fs := newFlagSet("list", stderr)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if fs.NArg() != 0 {
		return errf(stderr, "usage: clisnap list")
	}
	st := snapshot.Store{Dir: dir}
	names, err := st.List()
	if err != nil {
		return errf(stderr, "%v", err)
	}
	if len(names) == 0 {
		fmt.Fprintf(stdout, "no snapshots in %s\n", dir)
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tEXIT\tCOMMAND")
	for _, name := range names {
		snap, err := st.Load(name)
		if err != nil {
			return errf(stderr, "%v", err)
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\n", name, snap.Exit, shellJoin(snap.Cmd))
	}
	tw.Flush()
	return exitOK
}

// shellJoin renders an argv for display, quoting words that would not
// survive a shell round-trip unquoted.
func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\n\"'\\$&|;<>(){}*?#~`") {
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}

func cmdShow(args []string, dir string, stdout, stderr io.Writer) int {
	fs := newFlagSet("show", stderr)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if fs.NArg() != 1 {
		return errf(stderr, "usage: clisnap show <name>")
	}
	st := snapshot.Store{Dir: dir}
	data, err := st.Raw(fs.Arg(0))
	if err != nil {
		return errf(stderr, "%v", err)
	}
	stdout.Write(data)
	return exitOK
}

func cmdRm(args []string, dir string, stdout, stderr io.Writer) int {
	fs := newFlagSet("rm", stderr)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if fs.NArg() == 0 {
		return errf(stderr, "usage: clisnap rm <name...>")
	}
	st := snapshot.Store{Dir: dir}
	for _, name := range fs.Args() {
		if err := st.Delete(name); err != nil {
			return errf(stderr, "%v", err)
		}
		fmt.Fprintf(stdout, "removed %s\n", name)
	}
	return exitOK
}

func cmdRedact(args []string, dir string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("redact", stderr)
	redactFlag := fs.String("redact", "", "comma-separated redactor names, or 'none'")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if fs.NArg() != 0 {
		return errf(stderr, "usage: clisnap redact [--redact LIST] < input")
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return errf(stderr, "%v", err)
	}
	engine, err := redact.Build(resolveRedactors(*redactFlag, cfg), cfg.Rules)
	if err != nil {
		return errf(stderr, "%v", err)
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return errf(stderr, "read stdin: %v", err)
	}
	out, _ := engine.Apply(string(data))
	io.WriteString(stdout, out)
	return exitOK
}
