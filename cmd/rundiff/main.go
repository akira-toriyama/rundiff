// Command rundiff wraps a command and, on re-run, returns only what changed
// since the previous run — fixed / new / unchanged — instead of the full output
// an AI coding agent would otherwise re-read every fix→test iteration. Where
// pare cuts in the space direction (one run's output), rundiff cuts in the time
// direction (between runs), via an order-independent diff of normalized lines.
//
// All logic lives in internal/{delta,cache,runner,cli,version}; main only maps
// the CLI's resolved exit code to the process. See the README for the exit-code
// contract.
package main

import (
	"os"

	"github.com/akira-toriyama/rundiff/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
