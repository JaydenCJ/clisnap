# Contributing to clisnap

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go 1.22 or newer; there are no other dependencies of any kind.

```bash
git clone https://github.com/JaydenCJ/clisnap.git
cd clisnap
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary and drives the full record → check →
drift → update lifecycle against a real volatile command; it must finish by
printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (all 90 tests).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   packages (`redact`, `snapshot`, `diff`) rather than in the CLI layer.

## Ground rules

- Zero runtime dependencies is a core feature: the `go.mod` require list
  stays empty. Adding a dependency needs strong justification in the PR.
- No network calls, ever — clisnap runs commands and reads files, nothing
  else. No telemetry.
- Redaction rules must be conservative: a new default rule needs evidence
  that its false-positive rate on stable output is effectively zero,
  otherwise it ships as opt-in.
- The snapshot format is versioned; any change to it bumps the format
  version and keeps `v1` decoding intact.
- Code comments and doc comments are written in English.

## Reporting bugs

Please include the output of `clisnap --version`, the exact command line,
the snapshot file involved (or `clisnap show <name>` output), and a minimal
command that reproduces the issue.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
