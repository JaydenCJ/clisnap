// Command clisnap is the snapshot-testing CLI. All behavior lives in
// internal/cli so it can be tested in-process; main only wires the real
// process streams and exit code.
package main

import (
	"os"

	"github.com/JaydenCJ/clisnap/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
