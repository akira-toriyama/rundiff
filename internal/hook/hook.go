// Package hook is rundiff's third pure leaf: it decides whether a Claude Code
// Bash tool call should be re-pointed through rundiff. Bytes in (a PreToolUse
// hook event), bytes out (a hook response, or nil for "no decision"). Like
// internal/delta and internal/adapter it performs no I/O, reads no clock,
// imports no other local package, and never panics on arbitrary bytes; the CLI
// carries the bytes across. There is deliberately no import between hook and
// adapter in either direction — they mirror value shapes (a Target's Tool is a
// LABEL, reconciled by a drift-alarm test in internal/cli), not types.
//
// The bias is the adapter's, transposed. The adapter's failure mode is a false
// statement about a run; ours is a false statement about what the run WILL BE.
// A wrong rewrite silently changes the command the agent asked for — the agent
// reads an answer to a question it never posed, and unlike a bad claim there is
// no output left to check it against. So: WHEN UNSURE, DO NOT REWRITE. Three
// rules follow:
//
//  1. Refuse by construction, not by blocklist. Split gates every byte of the
//     command BEFORE it means anything, so the whole universe of shell syntax
//     (pipes, redirects, substitutions, globs, quotes, newlines) is out before
//     any pattern is consulted. A rewriter that enumerates what is dangerous
//     loses the day someone invents a new danger; one that enumerates what is
//     inert does not.
//  2. Whole-token prefix equality. A target matches argv[:len(Prefix)] token by
//     token — never a substring, never a basename. `go test` is a target;
//     `go-test-runner`, `mygo test` and `npm run go` are not.
//  3. Nothing whose stdout is consumed. rundiff REPLACES a command's stdout
//     with a JSON record plus a delta, so wrapping anything feeding a pipe, a
//     redirect or a `$(…)` would corrupt the consumer — and rundiff buffers with
//     no TTY, so wrapping a watcher hangs forever with zero output.
//
// # The money property
//
// safeByte permits only bytes that are INERT in POSIX-sh word context:
// [A-Za-z0-9] and _ - . / = : @ + , — no quote, no metacharacter, no byte >=
// 0x80. Every byte of the command is gated against that set before it is split,
// so for any string this package accepts:
//
//	"rundiff -- " + strings.Join(argv, " ")
//
// needs NO shell quoter — a real shell re-splits it into exactly ["rundiff",
// "--"] + argv. (`=` is the one byte with a context: it makes a leading word an
// assignment. It cannot bite here, because a rewritten command's leading word is
// always the rundiff binary, and an original whose argv[0] carries `=` is an env
// prefix we refuse outright.) TestSplit_matchesRealShell checks that claim
// against a real /bin/sh rather than against our model of one.
//
// That property is load-bearing twice over. It is why the rewrite is safe
// without quoting, and it is why rundiff's exec.Command receives clean DIRECT
// argv instead of `bash -lc '<blob>'` — which is exactly what keeps
// internal/adapter's whole-token gates able to read the command and its tsc /
// eslint argv hints able to fire. A hook that wrapped commands in a shell would
// silently blind the adapter it exists to feed.
package hook

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// binName is the basename that marks a command as already wrapped, whatever
// path it was invoked by. It is deliberately the literal name and not
// Options.Bin: idempotence must hold across a config change, so a command
// wrapped yesterday as `rundiff` must still read as wrapped today when Bin says
// `/opt/homebrew/bin/rundiff`.
const binName = "rundiff"

// Options configures the rewrite.
type Options struct {
	// Bin is the rundiff binary as it must appear in the rewritten string;
	// "" means "rundiff" (found on PATH). A caller passing an absolute path
	// gets it substituted into Print's guard, args and permission entries too.
	// This is trusted configuration, not agent input: it is expected to be a
	// safeByte token, and it is the ONE string in the output this package does
	// not re-derive from a gated command.
	Bin string
}

// Target is one wrappable command prefix, matched by whole-token equality
// against argv[:len(Prefix)]. Tool is the internal/adapter parser name the
// prefix is expected to produce — a LABEL only (a drift-alarm test in
// internal/cli asserts set(Tool) == adapter.Tools()); there is deliberately no
// import between hook and adapter in either direction.
type Target struct {
	Prefix []string
	Tool   string
}

// targets is the flat, closed list. Flat because each entry renders VERBATIM as
// one narrow permission entry in Print — a nested "launcher × tool" model would
// be tidier here and unusable there.
//
// Two exclusions are the whole point of the list, and both are silence, not
// oversight:
//
//   - `vitest` requires an explicit `run`. Bare `vitest` is WATCH mode; rundiff
//     buffers output and gives the child no TTY, so a wrapped watcher hangs
//     forever having printed nothing. The one failure mode worse than a wrong
//     answer is no answer, indefinitely.
//   - No `npm test` / `pnpm test` / `npm run <script>`. The script body is
//     invisible from argv, so rundiff cannot know it is not a watcher, a
//     multi-tool composite, or something whose stdout is piped inside the
//     script. (The adapter can still parse such a run's OUTPUT — reading what
//     came back is safe in a way that redirecting what will run is not.)
//
// And no `go build`: the adapter makes no claim for it and a successful build
// prints nothing, so wrapping it is pure cost against an empty delta.
var targets = []Target{
	{Prefix: []string{"go", "test"}, Tool: "go-test"},

	{Prefix: []string{"pytest"}, Tool: "pytest"},
	{Prefix: []string{"py.test"}, Tool: "pytest"},
	{Prefix: []string{"python", "-m", "pytest"}, Tool: "pytest"},
	{Prefix: []string{"python3", "-m", "pytest"}, Tool: "pytest"},

	{Prefix: []string{"jest"}, Tool: "jest"},
	{Prefix: []string{"npx", "jest"}, Tool: "jest"},
	{Prefix: []string{"pnpm", "exec", "jest"}, Tool: "jest"},

	{Prefix: []string{"vitest", "run"}, Tool: "vitest"},
	{Prefix: []string{"npx", "vitest", "run"}, Tool: "vitest"},
	{Prefix: []string{"pnpm", "exec", "vitest", "run"}, Tool: "vitest"},

	{Prefix: []string{"cargo", "test"}, Tool: "cargo-test"},

	{Prefix: []string{"tsc"}, Tool: "tsc"},
	{Prefix: []string{"npx", "tsc"}, Tool: "tsc"},
	{Prefix: []string{"pnpm", "exec", "tsc"}, Tool: "tsc"},

	{Prefix: []string{"eslint"}, Tool: "eslint"},
	{Prefix: []string{"npx", "eslint"}, Tool: "eslint"},
	{Prefix: []string{"pnpm", "exec", "eslint"}, Tool: "eslint"},
}

// Targets returns the wrappable command prefixes. The result is a deep copy: the
// list is this package's only shared data and a caller must not be able to widen
// what gets rewritten by mutating it.
func Targets() []Target {
	out := make([]Target, len(targets))
	for i, t := range targets {
		out[i] = Target{Prefix: slices.Clone(t.Prefix), Tool: t.Tool}
	}
	return out
}

// refused are flags that make a rewrite either useless or harmful, matched
// against a token exactly or in its flag=value form. Three families, one reason
// each:
//
//   - Watch / interactive (--watch, -w, --ui, -i, --pdb, --serve): the command
//     never exits, and a buffered TTY-less child never even prints.
//   - Machine output (--json, -json, --reporter, -f, --format, --silent): the
//     caller wants THAT format on stdout, and rundiff would replace it with its
//     own. It also puts the output beyond the adapter's parsers.
//   - Not-a-test-run (--collect-only, --co, -b, --build, --incremental, -fuzz,
//     -bench, -h, --help, --version): there is no failing/fixed/new to be had,
//     and --incremental / -b make even the tool's own output run-dependent.
//
// Go's flag package takes ONE or two dashes for the same flag, so both spellings
// are listed. That is not cosmetic: `go test -bench=. ./...` prints timings that
// differ every run (a delta of pure noise), and `go test -fuzz=Fuzz` runs until
// it is killed — the watcher hang this set exists to prevent, reached by a flag
// rather than a tool.
//
// Over-refusing costs a wrapper; under-refusing costs a hang or a corrupted
// pipe. The asymmetry decides every borderline case.
var refused = map[string]bool{
	"--watch": true, "-w": true, "--watchAll": true, "--watch-all": true,
	"--ui": true, "-i": true, "--interactive": true, "--pdb": true, "--serve": true,
	"--json": true, "-json": true, "--reporter": true, "-f": true, "--format": true,
	"--max-warnings": true, "--collect-only": true, "--co": true,
	"-b": true, "--build": true, "--incremental": true,
	"--fuzz": true, "-fuzz": true, "--bench": true, "-bench": true, "--silent": true,
	"-h": true, "--help": true, "--version": true,
}

// reCDPrefix is the ONE carve-out from "no shell syntax", and it is safe
// precisely BECAUSE it runs on the RAW string before the byte gate: it peels
// `cd <dir> && ` off the front and hands the REST to Split, where `&` is not a
// safe byte — so no second `&&`, no `;`, no pipe can survive in the tail. The
// directory token is confined to the same safe alphabet, so `cd /a b && …` (a
// space inside the token) does not match at all and falls through to the byte
// gate, which refuses it. `agent cd's into the web workspace and runs vitest` is
// far too common to give up; every other shell operator is not.
var reCDPrefix = regexp.MustCompile(`^cd ([A-Za-z0-9_.:/=@+,-]+) && (.+)$`)

// splitCDPrefix peels a leading `cd <dir> && ` off s. The prefix is echoed
// verbatim into the rewrite; the tail is what gets gated and wrapped.
func splitCDPrefix(s string) (prefix, tail string) {
	m := reCDPrefix.FindStringSubmatch(s)
	if m == nil {
		return "", s
	}
	return "cd " + m[1] + " && ", m[2]
}

// Command decides one command string. On ok it returns the rewritten string and
// an empty reason; on refusal it returns the reason (a diagnostic for the CLI,
// never part of the hook protocol) and ok=false. It is total: no input errors,
// panics, or reaches for the world.
func Command(s string, opt Options) (rewritten string, reason string, ok bool) {
	bin := opt.Bin
	if bin == "" {
		bin = binName
	}

	prefix, tail := splitCDPrefix(s)

	argv, split := Split(tail)
	if !split {
		return "", "unsplittable", false
	}

	// An env assignment in the command position (`GOFLAGS=-run=X go test`) is
	// refused, and that refusal IS the documented escape hatch: `RUNDIFF_HOOK=0
	// <cmd>` runs anything unwrapped. The variable is never read — its presence
	// in argv[0] is the whole mechanism, which is why the hatch keeps working
	// even when the hook binary cannot see the environment at all.
	if strings.Contains(argv[0], "=") {
		return "", "env-prefix", false
	}

	// Idempotence, by basename so it holds for any path the binary was reached
	// by. Without this a re-entrant hook (a retried tool call, a nested agent)
	// would build `rundiff -- rundiff -- go test`, whose cache key is a command
	// that is not the one anyone ran.
	if path.Base(argv[0]) == binName {
		return "", "already-wrapped", false
	}

	if tok, bad := refusedFlag(argv); bad {
		return "", "refused-flag:" + tok, false
	}

	if !matchTarget(argv) {
		return "", "not-a-target", false
	}

	// The join is canonical (single-space), not the original spacing: runs of
	// spaces and tabs collapse. That is deliberate — the same command typed with
	// different whitespace must land on ONE cache key, or the first re-run
	// silently diffs against nothing.
	return prefix + bin + " -- " + strings.Join(argv, " "), "", true
}

// refusedFlag reports the first token that is a refused flag, exactly or in its
// flag=value form, and returns the token as written (the reason is a diagnostic;
// echoing what the agent typed is more useful than echoing our table's key).
func refusedFlag(argv []string) (string, bool) {
	for _, tok := range argv {
		name := tok
		if i := strings.IndexByte(tok, '='); i >= 0 {
			name = tok[:i]
		}
		if refused[name] {
			return tok, true
		}
	}
	return "", false
}

// matchTarget reports whether argv begins with some target's prefix, compared
// token by token. Whole tokens only: a substring or basename match would let
// `./scripts/go test`, `mygo test` or a path argument named `tsc` through.
func matchTarget(argv []string) bool {
	for _, t := range targets {
		if len(argv) < len(t.Prefix) {
			continue
		}
		if slices.Equal(argv[:len(t.Prefix)], t.Prefix) {
			return true
		}
	}
	return false
}
