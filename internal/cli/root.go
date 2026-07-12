// Package cli is rundiff's cobra adapter: it parses flags, runs the wrapped
// command (internal/runner), loads/saves the per-key baseline (internal/cache),
// drives the pure diff (internal/delta), and writes the report to stdout. It
// holds no diff logic and owns the exit-code contract. main is just
// os.Exit(cli.Execute()).
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/akira-toriyama/rundiff/internal/adapter"
	"github.com/akira-toriyama/rundiff/internal/cache"
	"github.com/akira-toriyama/rundiff/internal/delta"
	"github.com/akira-toriyama/rundiff/internal/runner"
	"github.com/spf13/cobra"
)

// rundiff's exit-code contract (documented in the README). rundiff is a wrapper,
// so it PROPAGATES the wrapped command's exit code, and reserves conventional
// high codes for its OWN failures:
//
//	0..255  the wrapped command's exit code (propagated)
//	125     rundiff's own error (bad flags, cache/IO failure)
//	126     the command was found but is not executable
//	127     the command was not found
//	130     interrupted (Ctrl-C / SIGTERM) before the run completed
//
// A propagated 125/126/127/130 is indistinguishable from rundiff's own only by
// the number; the machine-readable JSON line on stdout disambiguates (rundiff's
// own errors emit no JSON line and print to stderr).
const (
	codeRundiff       = 125
	codeNotExecutable = 126
	codeNotFound      = 127
	codeInterrupted   = 130
)

// exitError couples an error with the exit code rundiff should return. An empty
// msg is silent (the JSON line already conveyed the outcome).
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// Execute builds the root command, runs it under a signal-cancellable context,
// and maps the outcome to rundiff's exit-code contract. Diagnostics go to stderr
// only, so a downstream `| jq` on stdout is never polluted.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := newRootCmd()
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		if ee.msg != "" {
			fmt.Fprintln(os.Stderr, "rundiff: "+ee.msg)
		}
		return ee.code
	}
	// A bare error here is a cobra flag/usage problem → rundiff's own error.
	fmt.Fprintln(os.Stderr, "rundiff: "+err.Error())
	return codeRundiff
}

type flags struct {
	json  bool
	raw   bool
	full  bool
	churn float64
	tool  string
}

func newRootCmd() *cobra.Command {
	var f flags

	root := &cobra.Command{
		Use:   "rundiff [flags] -- <command> [args...]",
		Short: "Diff a command's output against its previous run (fixed/new/unchanged)",
		Long: "rundiff — time-direction output diffing for AI coding agents.\n\n" +
			"Runs <command>, captures its combined output, and on a re-run for the same\n" +
			"cache key (argv + cwd + git branch) prints only what changed since last time,\n" +
			"as an order-independent diff of normalized lines — so a reordered test run,\n" +
			"changed timestamps, elapsed times and temp paths do NOT show up as changes.\n" +
			"Line 1 is always a machine-readable JSON object (transition, added, removed,\n" +
			"unchanged, …). Where pare cuts one run's output down, rundiff cuts between runs.\n\n" +
			"The first run for a key establishes a baseline and echoes its output; each\n" +
			"later run reports the delta. When the delta cannot be trusted (binary output,\n" +
			"huge churn, torn parallel output) rundiff degrades to a bounded full view and\n" +
			"says so — it never shows a delta it does not trust.",
		Example: "  # in a fix → test loop, see only what changed each run\n" +
			"  rundiff -- go test ./...\n\n" +
			"  # machine-readable for an agent / script\n" +
			"  rundiff --json -- pnpm test\n\n" +
			"  # compare raw output with no noise-cancelling normalization\n" +
			"  rundiff --raw -- ./flaky.sh",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWrap(cmd, args, f)
		},
	}

	// Stop parsing rundiff flags at the first positional so the wrapped command's
	// own flags are never consumed (`rundiff -- pnpm --json` keeps --json for pnpm).
	root.Flags().SetInterspersed(false)
	root.Flags().BoolVar(&f.json, "json", false, "emit the whole record as one JSON object (added_lines/removed_lines instead of a text body)")
	root.Flags().BoolVar(&f.raw, "raw", false, "compare raw lines with no noise-cancelling normalization")
	root.Flags().BoolVar(&f.full, "full", false, "show the bounded full current output as the body, even when a trusted delta exists")
	root.Flags().Float64Var(&f.churn, "churn", 0.5, "degrade to full output when the changed fraction reaches this (0..1)")
	root.Flags().StringVar(&f.tool, "tool", "",
		"force ("+strings.Join(adapter.Tools(), "|")+") or disable (none) the file-level failure adapter; default: auto-detect (abstains when unsure)")

	root.Version = versionLine()
	root.SetVersionTemplate("rundiff {{.Version}}\n")
	root.AddCommand(newVersionCmd())
	return root
}

func runWrap(cmd *cobra.Command, args []string, f flags) error {
	if len(args) == 0 {
		return &exitError{code: codeRundiff, msg: "no command given (usage: rundiff [flags] -- <command> [args...])"}
	}
	if f.churn < 0 || f.churn > 1 {
		return &exitError{code: codeRundiff, msg: "--churn must be between 0 and 1"}
	}
	if f.tool != "" && f.tool != "none" && !slices.Contains(adapter.Tools(), f.tool) {
		return &exitError{code: codeRundiff,
			msg: "unknown --tool " + f.tool + " (known: " + strings.Join(adapter.Tools(), ", ") + ", none)"}
	}

	ctx := cmd.Context()
	res, err := runner.Run(ctx, args)
	if err != nil {
		switch {
		case errors.Is(err, runner.ErrNotFound):
			return &exitError{code: codeNotFound, msg: "command not found: " + args[0]}
		case errors.Is(err, runner.ErrNotExecutable):
			return &exitError{code: codeNotExecutable, msg: "command not executable: " + args[0]}
		case errors.Is(err, runner.ErrCancelled):
			return &exitError{code: codeInterrupted, msg: "interrupted"}
		default:
			return &exitError{code: codeRundiff, msg: "running command: " + err.Error()}
		}
	}

	cwd, _ := os.Getwd()
	branch := runner.GitBranch(ctx, cwd)
	key := cache.Key(args, cwd, branch)
	dir, dirErr := cache.Dir()
	if dirErr != nil {
		return &exitError{code: codeRundiff, msg: "resolving cache dir: " + dirErr.Error()}
	}

	var prev *delta.Run
	var ageSeconds int
	now := time.Now().Unix()
	if entry, ok, loadErr := cache.Load(dir, key); loadErr != nil {
		// A corrupt cache entry is not fatal: treat it as no baseline and warn.
		fmt.Fprintln(os.Stderr, "rundiff: ignoring unreadable baseline: "+loadErr.Error())
	} else if ok {
		prev = &delta.Run{Output: entry.Output, Exit: entry.Exit}
		if a := now - entry.CreatedAt; a > 0 {
			ageSeconds = int(a)
		}
	}

	// The file-level adapter re-parses both runs' raw bytes and abstains (nil)
	// whenever it is unsure — a nil claim renders as null fields, never as [].
	var claim *delta.FileClaim
	if c := extractClaim(args, prev, res, f.tool); c != nil {
		claim = &delta.FileClaim{Tool: c.Tool, Failing: c.Failing, Fixed: c.Fixed, New: c.New}
	}

	// f.churn defaults to 0.5 (the flag default) and is passed by pointer so an
	// explicit `--churn 0` reaches the core as 0 (degrade on any change) instead
	// of colliding with the unset sentinel.
	opt := delta.Options{JSON: f.json, Raw: f.raw, Full: f.full, ChurnLimit: &f.churn}
	rep := delta.Diff(prev, delta.Run{Output: res.Output, Exit: res.Exit},
		delta.Meta{AgeSeconds: ageSeconds, Key: key[:12], FileClaim: claim}, opt)
	line, body := delta.Render(rep, opt)

	out := cmd.OutOrStdout()
	if _, err := out.Write(line); err != nil {
		return &exitError{code: codeRundiff, msg: "writing output: " + err.Error()}
	}
	if body != "" {
		fmt.Fprintln(out, body)
	}

	// Update the baseline to this run. A save failure is non-fatal (the diff was
	// already reported); warn so a persistently broken cache is visible.
	if err := cache.Save(dir, key, &cache.Entry{
		Argv: args, Cwd: cwd, Branch: branch, Exit: res.Exit, Output: res.Output, CreatedAt: now,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "rundiff: could not save baseline: "+err.Error())
	}

	// Propagate the wrapped command's exit code. A signal death (-1) maps to 128.
	childExit := res.Exit
	if childExit < 0 {
		childExit = 128
	}
	if childExit != 0 {
		return &exitError{code: childExit} // silent: the JSON line already reported it
	}
	return nil
}

// extractClaim adapts the runner/cache values into adapter.Run and back —
// delta and adapter are import-free leaves that mirror shapes, so the CLI is
// where the values cross. Selection-relevant environment variables are lifted
// into tokens so env-injected flags (GOFLAGS=-run=…, PYTEST_ADDOPTS=-k …)
// reach the adapter's blocked/selection gates: they change the tool's
// behavior without changing argv or the cache key.
func extractClaim(args []string, prev *delta.Run, res runner.Result, forceTool string) *adapter.Claim {
	var prevA *adapter.Run
	if prev != nil {
		prevA = &adapter.Run{Output: prev.Output, Exit: prev.Exit}
	}
	// Pass the selection-relevant env vars raw; the adapter applies each only
	// to the tool that owns it (GOFLAGS→go, PYTEST_ADDOPTS→pytest), so a Go env
	// var never withholds a pytest claim. The var list is the adapter's, so the
	// two sides cannot drift.
	env := map[string]string{}
	for _, name := range adapter.EnvVarNames() {
		env[name] = os.Getenv(name)
	}
	return adapter.Extract(args, env, prevA, adapter.Run{Output: res.Output, Exit: res.Exit}, forceTool)
}
