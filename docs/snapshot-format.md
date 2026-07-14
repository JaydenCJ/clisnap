# The clisnap snapshot format (v1)

Snapshots are plain text files, one per recorded command, stored in the
snapshot directory (`.clisnap/` by default) and meant to be committed to
version control and read in code review like any other test fixture.

## Example

```text
clisnap snapshot v1
cmd: ["./greet.sh"]
exit: 0
redact: ansi,tmp-path,home-path,timestamp,uuid,hex-addr,duration,pid
--- stdout: 3 lines ---
|greeter 1.0
|started <TIMESTAMP> pid <PID>
|hello, world
--- stderr: 0 lines ---
```

## Layout

| Line | Meaning |
| --- | --- |
| `clisnap snapshot v1` | magic + format version; decoding is strict per version |
| `cmd: <JSON array>` | the exact argv that was executed and will be re-executed by `check` |
| `exit: <int>` | recorded exit code — part of the assertion, not an error |
| `redact: <names or ->` | comma-separated redactor names active at record time; `-` means none |
| `--- stdout: N lines [...] ---` | section header; the optional suffix is `(no trailing newline)` |
| `\|<content>` | exactly N lines follow, each prefixed with a single `\|` |
| `--- stderr: ... ---` | same encoding for stderr |

## Design notes

**Why `|` prefixes?** Output can contain anything — blank lines, lines that
look exactly like section headers, trailing whitespace. Prefixing every
content line makes the framing unambiguous with zero escaping, and makes
trailing whitespace visible in review.

**Why record the redactor list?** `check` re-applies *the snapshot's own*
rule set, never the current defaults. Upgrading clisnap or editing the
project config can therefore never silently change what an existing
snapshot asserts.

**Why track the missing trailing newline?** `printf 'done'` and
`echo done` are different behaviors. The section header records the
difference, and diffs render it git-style with
`\ No newline at end of file`.

**Why is decoding strict?** A snapshot mangled by a merge conflict or a
hand edit must fail with a positioned error (`snapshot line 7: ...`)
rather than compare garbage and produce a confusing test result.

**Custom rules are referenced, not inlined.** A snapshot recorded with a
custom rule (say `build-id`) stores only the name. If the rule disappears
from `.clisnap/config.json`, `check` fails with a hard error telling you
which rule is missing — it does not silently skip redaction.
