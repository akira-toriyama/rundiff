package hook

import (
	"bytes"
	"encoding/json"
	"path"
	"strings"
)

// The settings.json shape Print emits. Structs, not maps, so field ORDER is
// stable — this is config a human pastes and a golden test pins.
type settings struct {
	Hooks       settingsHooks       `json:"hooks"`
	Permissions settingsPermissions `json:"permissions"`
}

type settingsHooks struct {
	PreToolUse []matcherEntry `json:"PreToolUse"`
}

type matcherEntry struct {
	Matcher string         `json:"matcher"`
	Hooks   []handlerEntry `json:"hooks"`
}

type handlerEntry struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Timeout int      `json:"timeout"`
}

type settingsPermissions struct {
	Allow []string `json:"allow"`
}

// guard builds the shell one-liner the hook runs. Every part of it is load-bearing:
//
//   - /bin/sh in EXEC form (command + args), not a bare string handed to the
//     user's login shell. A login shell sources the user's rc files, and anything
//     they print — a version-manager banner, a fortune, a `set -x` trace — lands
//     on the hook's STDOUT, where Claude Code is expecting JSON and nothing else.
//     A known POSIX shell with -c cannot be polluted that way.
//   - `command -v rundiff` (or `[ -x /abs/rundiff ]`): without the guard, a user
//     who has the hook configured but not the binary installed gets an error on
//     EVERY Bash call they make. A missing rundiff has to be a silent no-op.
//   - `|| exit 0`, and deliberately NO `exec`. `exec rundiff hook rewrite` would
//     replace the shell, so a rundiff too OLD to have the subcommand would exit
//     non-zero from inside the exec'd process and the `|| exit 0` could never
//     run. Keeping rundiff as a child keeps the fallback reachable, which is what
//     makes an upgrade-lagged install degrade to "no rewrite" instead of "an
//     error on every command".
func guard(bin string) string {
	if path.IsAbs(bin) {
		return "[ -x " + bin + " ] && " + bin + " hook rewrite || exit 0"
	}
	return "command -v " + bin + " >/dev/null 2>&1 && " + bin + " hook rewrite || exit 0"
}

// permissions renders ONE narrow entry per target, straight from Targets().
//
// The entries exist because the hook returns no permissionDecision (see
// response): Claude Code re-checks permissions against the REWRITTEN string, and
// a user's existing `Bash(go test:*)` does NOT cover `rundiff -- go test …`. So
// the rewrite would prompt on every run without them.
//
// They are narrow because the obvious shortcut is a security hole. `Bash(rundiff:*)`
// approves rundiff with ANY argv, and rundiff execs whatever argv it is handed —
// that single entry is `Bash(*)` wearing a costume. Generating from Targets()
// means the allowed set can never drift wider than what the hook can actually
// produce.
func permissions(bin string) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, "Bash("+bin+" -- "+strings.Join(t.Prefix, " ")+":*)")
	}
	return out
}

// Print renders the settings.json fragment that installs the hook. asJSON gives
// the machine form (valid JSON, ready to merge); otherwise the same JSON with a
// `#` commentary above it, because a user is being asked to grant permissions and
// deserves to read WHY before they do.
func Print(opt Options, asJSON bool) []byte {
	bin := opt.Bin
	if bin == "" {
		bin = binName
	}

	doc := settings{
		Hooks: settingsHooks{PreToolUse: []matcherEntry{{
			Matcher: "Bash",
			Hooks: []handlerEntry{{
				Type:    "command",
				Command: "/bin/sh",
				Args:    []string{"-c", guard(bin)},
				Timeout: 5,
			}},
		}}},
		Permissions: settingsPermissions{Allow: permissions(bin)},
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// No HTML escaping: the guard contains `>` and `&&`, and a settings file full
	// of > escapes is unreadable and unmaintainable for the human who has to
	// keep it.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil
	}
	body := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))

	if asJSON {
		return body
	}
	return append([]byte(commentary(bin)), body...)
}

// commentary is the human preamble. It documents the REFUSALS as carefully as
// the rewrites, because "why didn't it wrap my command?" is the question this
// hook will be asked most, and the answer is almost always deliberate.
func commentary(bin string) string {
	var b strings.Builder
	w := func(lines ...string) {
		for _, l := range lines {
			b.WriteString(strings.TrimRight("# "+l, " "))
			b.WriteByte('\n')
		}
	}
	w(
		"rundiff PreToolUse hook — merge into .claude/settings.json.",
		"",
		"WHAT IT REWRITES. A Bash command whose argv begins with one of the test",
		"commands listed under permissions.allow below (go test, pytest, jest,",
		"vitest run, cargo test, tsc, eslint, and their npx / pnpm exec / python -m",
		"forms) is re-pointed through rundiff: `go test ./...` runs as",
		"`"+bin+" -- go test ./...`. A leading `cd <dir> && ` is carried across",
		"unchanged.",
		"",
		"WHAT IT CONSPICUOUSLY DOES NOT. Anything containing a pipe, a redirect, a",
		"`&&` chain (other than the leading `cd`), a glob, a quote, a `$(…)`, or a",
		"non-ASCII byte is left exactly as typed — as is any command with an env",
		"prefix (`FOO=1 go test`), any `npm test` / `pnpm test` / `npm run <script>`,",
		"and anything carrying a watch, interactive, or machine-output flag",
		"(--watch, --ui, --json, --reporter, -f, --collect-only, --help, …).",
		"",
		"The reason is one sentence: rundiff REPLACES a command's stdout with a JSON",
		"record plus a delta of what changed. So rewriting a command whose stdout is",
		"consumed downstream — piped, redirected, captured — would feed the consumer",
		"rundiff's output instead of the tool's and break it. `npm test` hides its",
		"script body from argv, so it may be a watcher or a pipeline and cannot be",
		"vouched for. A watch flag is worse still: rundiff buffers and gives the",
		"child no TTY, so a wrapped watcher hangs forever having printed nothing.",
		"Bare `vitest` IS watch mode, which is why only `vitest run` is listed.",
		"",
		"ESCAPE HATCHES. Two, and neither needs this file edited:",
		"  RUNDIFF_HOOK=0 go test ./...   — an env prefix is never rewritten.",
		"  "+bin+" --full -- go test ./...   — reprints the last full output from",
		"                                    the cache without re-running anything.",
		"",
		"THE `command -v` GUARD. The hook is a no-op when rundiff is not installed.",
		"Without it, every Bash call in every session would fail on a missing binary.",
		"There is deliberately no `exec` in the command string, so `|| exit 0` stays",
		"reachable for a rundiff too old to have the `hook rewrite` subcommand — an",
		"upgrade-lagged install degrades to 'no rewrite', not to 'an error on every",
		"command'. /bin/sh is invoked in exec form so the user's login profile cannot",
		"print anything onto the hook's stdout, which must be JSON and nothing else.",
		"",
		"WHY THE PERMISSION ENTRIES. The hook never returns a permissionDecision — it",
		"only supplies updatedInput — so Claude Code re-checks permissions against the",
		"REWRITTEN command string. An existing `Bash(go test:*)` does NOT cover",
		"`"+bin+" -- go test ./...`, so without these entries every wrapped command",
		"would prompt. Each entry is one target, verbatim.",
		"",
		"⚠ NEVER collapse them into `Bash("+bin+":*)`. rundiff execs whatever argv it",
		"is handed, so that single entry approves every command on the machine — it is",
		"`Bash(*)` in a costume. The narrow entries are the only thing standing",
		"between this hook and a blanket exec grant.",
		"",
		"The `cd <dir> && <test cmd>` form additionally needs whatever `Bash(cd:*)`",
		"rule you already use for `cd` — this file does not grant it.",
		"",
	)
	return b.String()
}
