# clisnap examples

This directory contains a deliberately volatile demo tool and a sample
project config. Everything runs offline.

## 1. Snapshot a volatile command

`sysreport.sh` prints a timestamp, its PID, a fresh temp directory and a
random-ish duration on every run — the exact output is never the same
twice, which is what breaks naive golden files.

```bash
cd examples
clisnap record sysreport -- ./sysreport.sh
clisnap check            # passes: the volatility was redacted away
./sysreport.sh           # run it yourself: different pid/time/tmpdir
clisnap check            # still passes
```

Look at the recorded file — the shape is asserted, the noise is not:

```bash
clisnap show sysreport
```

## 2. Catch a real change

Edit `sysreport.sh` and change `status: healthy` to `status: degraded`,
then:

```bash
clisnap check            # FAILs with a unified diff, exit code 1
clisnap check --update   # accept the new behavior
```

## 3. Project-specific redaction rules

`config.json` shows a custom rule that stabilizes a build-id token. Copy
it into your snapshot directory to use it:

```bash
mkdir -p .clisnap && cp config.json .clisnap/
clisnap record build -- ./sysreport.sh
grep 'build-<ID>' .clisnap/build.snap
```

Custom rules run before the built-ins and are recorded into each
snapshot's `redact:` header by name, so checks stay reproducible.
