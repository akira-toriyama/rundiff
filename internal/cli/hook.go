package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/akira-toriyama/rundiff/internal/hook"
	"github.com/spf13/cobra"
)

// maxEventBytes bounds the hook event read from stdin. The event is a small
// JSON object; anything larger is not one, and the hook runs on every Bash tool
// call, so it must not be a place where memory can grow.
const maxEventBytes = 1 << 20

// newHookCmd builds `rundiff hook`, the Claude Code integration:
//
//	rundiff hook rewrite   PreToolUse event on stdin → hook response on stdout
//	rundiff hook print     the settings.json snippet that registers it
//
// This is the one place rundiff's stdout is NOT its own run record: `hook
// rewrite` speaks Claude Code's hook schema (a schema rundiff does not own), and
// `hook print` prints a config snippet. The wrapper-mode contract — line 1 is
// always the run record — is unchanged for `rundiff -- <cmd>`.
//
// `hook rewrite` is TOTAL: it never fails. Every malformed, unknown or
// unwrappable input yields zero bytes and exit 0, because this process runs
// before every single Bash command an agent issues. It must never exit 2 (the
// hook protocol's "block the tool call" code), never write a partial object, and
// never print to stdout what is not a hook response. A hook that cannot decide
// is a hook that does nothing.
func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Claude Code integration (auto-wrap test commands with `rundiff --`)",
		Long: "rundiff hook — the Claude Code PreToolUse integration.\n\n" +
			"With the hook registered, an agent that types `go test ./...` runs\n" +
			"`rundiff -- go test ./...` and reads the delta from its previous run,\n" +
			"without having to remember the prefix. Only a metacharacter-free direct\n" +
			"invocation of a recognized test tool is rewritten — never a pipeline, a\n" +
			"redirect, an `npm test` (whose script body is invisible and may be a\n" +
			"watcher), or anything else whose stdout something downstream reads.\n\n" +
			"`rundiff hook print` writes the settings.json snippet; rundiff never edits\n" +
			"your Claude Code config itself.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help() // a bare `rundiff hook` explains itself
		},
	}
	cmd.AddCommand(newHookRewriteCmd(), newHookPrintCmd())
	return cmd
}

func newHookRewriteCmd() *cobra.Command {
	var (
		explain string
		bin     string
	)
	cmd := &cobra.Command{
		Use:   "rewrite",
		Short: "Read a PreToolUse event on stdin, write the hook response on stdout",
		Long: "Reads one Claude Code PreToolUse event on stdin and, when the command is a\n" +
			"recognized test invocation, writes a hook response that re-runs it under\n" +
			"rundiff. Anything else writes nothing at all.\n\n" +
			"Always exits 0: it runs before every Bash command an agent issues, so a\n" +
			"failure here must never block one. RUNDIFF_HOOK=0 in the environment turns\n" +
			"it off entirely; RUNDIFF_HOOK_DEBUG=1 prints why a command was left alone\n" +
			"(to stderr — stdout stays the hook's channel).",
		Example: "  # what the hook does, without a hook\n" +
			"  rundiff hook rewrite --explain 'go test ./...'\n" +
			"  rundiff hook rewrite --explain 'npm test'",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opt := hook.Options{Bin: bin}
			if explain != "" {
				return explainCommand(cmd, explain, opt)
			}
			runRewrite(cmd, opt)
			return nil
		},
	}
	cmd.Flags().StringVar(&explain, "explain", "",
		"decide this command string and print the verdict instead of speaking the hook protocol")
	// --bin must agree with the value baked into `hook print --bin`: the rewrite
	// emits `<bin> -- <cmd>`, and Claude Code checks THAT string against the
	// permission entries `hook print` generated from the same bin. The guard
	// `hook print --bin` emits already passes this through, so a hand-registered
	// hook and a printed one stay consistent.
	cmd.Flags().StringVar(&bin, "bin", "",
		"the rundiff path to emit in the rewritten command (must match `hook print --bin`); default: `rundiff`")
	return cmd
}

// runRewrite is the hook path. It has no failure mode by construction: it writes
// a hook response or it writes nothing, and it returns no error either way.
func runRewrite(cmd *cobra.Command, opt hook.Options) {
	// The off switch is read BEFORE stdin: a disabled hook does not even look at
	// the event.
	if os.Getenv("RUNDIFF_HOOK") == "0" {
		return
	}
	// A panic in the leaf would still be a hook that broke the agent's command,
	// so the totality claim is belt-and-braces: the leaf is fuzzed not to panic,
	// and if it ever does, the hook still says nothing.
	defer func() { _ = recover() }()

	event, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), maxEventBytes))
	if err != nil {
		return
	}
	out := hook.Rewrite(event, opt)
	if out == nil {
		debugRefusal(cmd, event, opt)
		return
	}
	// One write of one complete object: a partial hook response is worse than
	// none (Claude Code would parse a truncated object as a failed hook).
	_, _ = cmd.OutOrStdout().Write(out)
}

// debugRefusal explains a non-rewrite on STDERR when RUNDIFF_HOOK_DEBUG=1.
// stdout belongs to the hook protocol and must stay empty here — that is what
// "no decision" looks like on the wire.
func debugRefusal(cmd *cobra.Command, event []byte, opt hook.Options) {
	if os.Getenv("RUNDIFF_HOOK_DEBUG") != "1" {
		return
	}
	errOut := cmd.ErrOrStderr()
	c := hook.EventCommand(event)
	if c == "" {
		fmt.Fprintln(errOut, "rundiff hook: not a PreToolUse/Bash event with a command")
		return
	}
	_, reason, _ := hook.Command(c, opt)
	fmt.Fprintf(errOut, "rundiff hook: left alone (%s): %s\n", reason, c)
}

// explainCommand is the human path: it answers "would you rewrite this?" without
// the hook protocol, so the predicate can be inspected from a terminal.
func explainCommand(cmd *cobra.Command, s string, opt hook.Options) error {
	out := cmd.OutOrStdout()
	rewritten, reason, ok := hook.Command(s, opt)
	if !ok {
		fmt.Fprintf(out, "left alone (%s): %s\n", reason, s)
		return nil
	}
	fmt.Fprintf(out, "rewrite: %s\n", rewritten)
	return nil
}

func newHookPrintCmd() *cobra.Command {
	var (
		asJSON bool
		bin    string
	)
	cmd := &cobra.Command{
		Use:   "print",
		Short: "Print the settings.json snippet that registers the hook",
		Long: "Prints the Claude Code settings.json snippet to STDOUT. rundiff does not\n" +
			"write your config: merge it into ~/.claude/settings.json (user scope) or\n" +
			".claude/settings.json (project scope) yourself, or let your dotfiles do it.\n\n" +
			"The snippet also carries the narrow permission entries the rewrite needs.\n" +
			"The hook never returns permissionDecision — it does not approve anything on\n" +
			"your behalf — so Claude Code checks the REWRITTEN command against your own\n" +
			"rules, and an existing `Bash(go test:*)` does not cover\n" +
			"`rundiff -- go test ./...`.",
		Example: "  rundiff hook print              # commented, for a human to paste\n" +
			"  rundiff hook print --json       # bare object, for jq/chezmoi to merge\n" +
			"  rundiff hook print --bin /opt/homebrew/bin/rundiff",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write(hook.Print(hook.Options{Bin: bin}, asJSON))
			if err != nil {
				return &exitError{code: codeRundiff, msg: "writing snippet: " + err.Error()}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the bare mergeable JSON object (no comments)")
	cmd.Flags().StringVar(&bin, "bin", "",
		"absolute path to the rundiff binary (default: resolve `rundiff` on PATH at hook time)")
	return cmd
}
