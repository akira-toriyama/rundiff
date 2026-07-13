package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/rundiff/internal/adapter"
	"github.com/akira-toriyama/rundiff/internal/hook"
)

// runHook drives the root command with stdin wired, returning stdout, stderr and
// the mapped exit code — the three channels the hook protocol cares about.
func runHook(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	root := newRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		return out.String(), errOut.String(), 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return out.String(), errOut.String(), ee.code
	}
	return out.String(), errOut.String(), codeRundiff
}

const goTestEvent = `{"session_id":"s","cwd":"/repo","hook_event_name":"PreToolUse","tool_name":"Bash",` +
	`"tool_input":{"command":"go test ./...","description":"Run tests","timeout":600000}}`

func TestHookRewrite_wiring(t *testing.T) {
	out, errOut, code := runHook(t, goTestEvent, "hook", "rewrite")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — the hook runs before every Bash call and must never fail one", code)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty on the hook path", errOut)
	}
	var got struct {
		HookSpecificOutput struct {
			HookEventName     string          `json:"hookEventName"`
			UpdatedInput      json.RawMessage `json:"updatedInput"`
			AdditionalContext string          `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\ngot: %s", err, out)
	}
	if got.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q", got.HookSpecificOutput.HookEventName)
	}
	var ui map[string]any
	if err := json.Unmarshal(got.HookSpecificOutput.UpdatedInput, &ui); err != nil {
		t.Fatal(err)
	}
	if ui["command"] != "rundiff -- go test ./..." {
		t.Errorf("command = %v, want the wrapped form", ui["command"])
	}
	// The whole tool_input is echoed: dropping `timeout` would quietly cut a
	// ten-minute suite back to the Bash tool's default and look like a rundiff bug.
	if ui["timeout"] != float64(600000) || ui["description"] != "Run tests" {
		t.Errorf("updatedInput lost a field: %v", ui)
	}
	if got.HookSpecificOutput.AdditionalContext == "" {
		t.Error("additionalContext empty: an agent that did not type `rundiff` has no way to read the output")
	}
	// The one field that must never appear: it would approve the command on the
	// user's behalf, and rundiff execs whatever argv it is handed.
	if strings.Contains(out, "permissionDecision") {
		t.Errorf("stdout carries permissionDecision — the hook must never approve anything:\n%s", out)
	}
}

// Every path that is not "a command I can express as direct argv" writes zero
// bytes and exits 0. Writing nothing is how "no decision" is spelled on the wire.
func TestHookRewrite_failsOpen(t *testing.T) {
	cases := []struct {
		name  string
		stdin string
	}{
		{"garbage", "not json at all"},
		{"truncated", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_i`},
		{"empty", ""},
		{"other event", `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`},
		{"other tool", `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"command":"go test ./..."}}`},
		{"background", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./...","run_in_background":true}}`},
		{"pipeline", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./... | tee log"}}`},
		{"not a target", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`},
		{"already wrapped", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rundiff -- go test ./..."}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, errOut, code := runHook(t, c.stdin, "hook", "rewrite")
			if code != 0 {
				t.Errorf("exit code = %d, want 0 (never 2 — that would BLOCK the agent's command)", code)
			}
			if out != "" {
				t.Errorf("stdout = %q, want zero bytes", out)
			}
			if errOut != "" {
				t.Errorf("stderr = %q, want empty (no noise on the hook path)", errOut)
			}
		})
	}
}

// RUNDIFF_HOOK=0 is the global off switch, checked before stdin is even read.
func TestHookRewrite_offSwitch(t *testing.T) {
	t.Setenv("RUNDIFF_HOOK", "0")
	out, _, code := runHook(t, goTestEvent, "hook", "rewrite")
	if code != 0 || out != "" {
		t.Errorf("code=%d out=%q, want 0 and zero bytes with RUNDIFF_HOOK=0", code, out)
	}
}

// The debug channel is stderr. stdout stays the hook's, always.
func TestHookRewrite_debugGoesToStderr(t *testing.T) {
	t.Setenv("RUNDIFF_HOOK_DEBUG", "1")
	ev := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`
	out, errOut, code := runHook(t, ev, "hook", "rewrite")
	if code != 0 || out != "" {
		t.Errorf("code=%d stdout=%q, want 0 and zero bytes", code, out)
	}
	if !strings.Contains(errOut, "not-a-target") || !strings.Contains(errOut, "npm test") {
		t.Errorf("stderr = %q, want the refusal reason", errOut)
	}
}

func TestHookRewrite_explain(t *testing.T) {
	for _, c := range []struct{ cmd, want string }{
		{"go test ./...", "rewrite: rundiff -- go test ./..."},
		{"npm test", "left alone (not-a-target)"},
		{"pytest -k \"a or b\"", "left alone (unsplittable)"},
		{"RUNDIFF_HOOK=0 go test ./...", "left alone (env-prefix)"},
	} {
		out, _, code := runHook(t, "", "hook", "rewrite", "--explain", c.cmd)
		if code != 0 {
			t.Errorf("--explain %q: exit %d", c.cmd, code)
		}
		if !strings.Contains(out, c.want) {
			t.Errorf("--explain %q = %q, want it to contain %q", c.cmd, out, c.want)
		}
	}
}

func TestHookPrint(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		args := []string{"hook", "print"}
		if asJSON {
			args = append(args, "--json")
		}
		out, _, code := runHook(t, "", args...)
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		// The entry that must never be OFFERED: rundiff execs arbitrary argv, so
		// `Bash(rundiff:*)` is `Bash(*)` in a costume. Naming it in a comment is
		// the opposite — that is the warning — so only the config itself is checked.
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue // a comment; the ⚠ warning is expected to name the pattern
			}
			for _, forbidden := range []string{"Bash(rundiff:*)", "Bash(rundiff *)"} {
				if strings.Contains(line, forbidden) {
					t.Errorf("snippet offers %s — that auto-approves `rundiff -- rm -rf /`\nline: %s", forbidden, line)
				}
			}
		}
		if !strings.Contains(out, "command -v rundiff") {
			t.Error("snippet lacks the missing-binary guard: an uninstalled rundiff would error on EVERY Bash call")
		}
	}

	// The --json form must be exactly one mergeable object, and its permission
	// list must cover every target — a target with no entry would prompt on every
	// run, and an entry with no target would grant more than rundiff rewrites.
	out, _, _ := runHook(t, "", "hook", "print", "--json")
	var snippet struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Command string   `json:"command"`
					Args    []string `json:"args"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(out), &snippet); err != nil {
		t.Fatalf("--json is not a JSON object: %v\n%s", err, out)
	}
	if len(snippet.Hooks.PreToolUse) != 1 || snippet.Hooks.PreToolUse[0].Matcher != "Bash" {
		t.Fatalf("want exactly one Bash matcher, got %+v", snippet.Hooks.PreToolUse)
	}
	h := snippet.Hooks.PreToolUse[0].Hooks[0]
	if h.Command != "/bin/sh" {
		t.Errorf("hook command = %q, want /bin/sh (a KNOWN shell: a login shell would source a profile whose output corrupts the hook's stdout)", h.Command)
	}
	if len(snippet.Permissions.Allow) != len(hook.Targets()) {
		t.Errorf("%d permission entries for %d targets — they must correspond 1:1",
			len(snippet.Permissions.Allow), len(hook.Targets()))
	}
}

// Drift alarm. hook and adapter are import-free leaves that must not know about
// each other, so nothing at runtime forces the hook's target list to match the
// tools the adapter can actually claim about. Drift is fail-safe (a target the
// adapter cannot parse simply yields no claim) but silent — so the build says it
// out loud: a new parser without a target, or a target no parser backs, fails
// here.
func TestHookTargets_coverAdapterTools(t *testing.T) {
	var labels []string
	for _, tgt := range hook.Targets() {
		if !slices.Contains(labels, tgt.Tool) {
			labels = append(labels, tgt.Tool)
		}
	}
	slices.Sort(labels)
	tools := adapter.Tools()
	if !slices.Equal(labels, tools) {
		t.Errorf("hook.Targets() tools = %v\nadapter.Tools()      = %v\nthey must be the same set", labels, tools)
	}
}

// Cobra resolution: `rundiff hook print` is the subcommand, but `rundiff --
// hook print` still wraps a program named `hook` — the `--` terminator is what
// separates rundiff's grammar from the wrapped command's, and adding a
// subcommand must not quietly shadow a user's binary.
func TestHook_doesNotShadowAWrappedCommandNamedHook(t *testing.T) {
	setCache(t)
	out, code := runCLI(t, "--", "hook", "print")
	if code != codeNotFound {
		t.Errorf("exit = %d, want %d (there is no program named `hook`; rundiff must have tried to RUN it, not run its own subcommand)", code, codeNotFound)
	}
	if strings.Contains(out, "PreToolUse") {
		t.Error("`rundiff -- hook print` printed the hook snippet: the subcommand shadowed the wrapped command")
	}
}

func TestHook_unknownSubcommandIsRundiffsOwnError(t *testing.T) {
	_, _, code := runHook(t, "", "hook", "bogus")
	if code != codeRundiff {
		t.Errorf("exit = %d, want %d (a mistyped subcommand is a config error the user must see)", code, codeRundiff)
	}
}
