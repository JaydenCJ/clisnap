# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-12

### Added

- `clisnap record <name> -- <cmd>`: run a command, capture stdout, stderr and
  exit code, redact volatile spans, and save a reviewable text snapshot.
- `clisnap check [--update] [name...]`: re-run stored commands, redact fresh
  output with each snapshot's own recorded rule set, and report unified diffs
  on mismatch; `--update` rewrites failing snapshots in place.
- Built-in volatility redactors: `ansi`, `tmp-path`, `home-path`, `timestamp`,
  `uuid`, `hex-addr`, `duration`, `pid` (default set) plus opt-in `date` and
  `epoch`. Idempotent, order-canonical, conservative by design.
- Dynamic home-directory redaction: the current user's real home path is
  redacted on any OS layout, so snapshots survive machine changes.
- Custom project rules and a default redactor list via `.clisnap/config.json`,
  parsed strictly (unknown keys rejected).
- Versioned, line-oriented snapshot format (`clisnap snapshot v1`) with
  byte-exact round-trips, missing-newline tracking and positioned parse errors.
- Myers O(ND) unified diff with prefix/suffix trimming, git-style
  `\ No newline at end of file` markers and a size-capped fallback.
- `clisnap list`, `show`, `rm`, and a stdin/stdout `redact` filter mode.
- `record --shell` to snapshot whole shell pipelines, and `--redact none`
  for verbatim golden files.
- 90 deterministic offline tests (`go test ./...`) and an end-to-end
  `scripts/smoke.sh` that prints `SMOKE OK`.

[0.1.0]: https://github.com/JaydenCJ/clisnap/releases/tag/v0.1.0
