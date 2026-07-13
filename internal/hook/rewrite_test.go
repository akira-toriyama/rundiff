package hook

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// event builds a PreToolUse event with the given tool_input body (raw JSON).
func preToolUse(toolInput string) []byte {
	return []byte(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":` + toolInput + `}`)
}

func TestRewrite(t *testing.T) {
	cases := []struct {
		name  string
		event string
		want  bool // want a response
	}{
		{"happy path", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`, true},
		{"not PreToolUse", `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`, false},
		{"no event name", `{"tool_name":"Bash","tool_input":{"command":"go test ./..."}}`, false},
		{"not Bash", `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"command":"go test ./..."}}`, false},
		// A backgrounded call is already detached from its output: the delta would
		// go nowhere and the cache would record a run nobody read.
		{"run_in_background", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./...","run_in_background":true}}`, false},
		{"run_in_background false", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./...","run_in_background":false}}`, true},
		// A shape we do not understand is a refusal, not a default.
		{"run_in_background not a bool", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./...","run_in_background":"yes"}}`, false},
		{"missing tool_input", `{"hook_event_name":"PreToolUse","tool_name":"Bash"}`, false},
		{"null tool_input", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":null}`, false},
		{"missing command", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"timeout":600000}}`, false},
		{"command is a number", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":42}}`, false},
		{"command is null", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":null}}`, false},
		{"command refused", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`, false},
		{"truncated JSON", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_inp`, false},
		{"empty", ``, false},
		{"not an object", `[1,2,3]`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Rewrite([]byte(c.event), Options{})
			if (got != nil) != c.want {
				t.Fatalf("Rewrite() = %s, want response=%v", got, c.want)
			}
			if got == nil {
				return
			}
			if !json.Valid(got) {
				t.Fatalf("invalid JSON: %s", got)
			}
		})
	}
}

// The response echoes the WHOLE tool input, byte for byte, and replaces only
// `command`. Dropping `timeout` would silently revert a deliberately-extended
// suite to the Bash default and kill it mid-run — a "diff" that ate the test
// run. An unknown field must survive for the same reason: this package cannot
// know what a future Claude Code puts there, and dropping what it does not
// understand is exactly the class of silent behavior change it exists to avoid.
func TestRewrite_echoesEveryField(t *testing.T) {
	ev := preToolUse(`{"command":"go test ./...","timeout":600000,"description":"Run the tests","future_field":{"a":[1,2]}}`)
	got := Rewrite(ev, Options{})
	if got == nil {
		t.Fatal("nil response")
	}

	var resp struct {
		HookSpecificOutput struct {
			HookEventName     string                     `json:"hookEventName"`
			UpdatedInput      map[string]json.RawMessage `json:"updatedInput"`
			AdditionalContext string                     `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(got, &resp); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, got)
	}
	hso := resp.HookSpecificOutput

	if hso.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName=%q want PreToolUse", hso.HookEventName)
	}
	if hso.AdditionalContext == "" {
		t.Error("additionalContext is empty")
	}

	echoed := map[string]string{
		"timeout":      `600000`,
		"description":  `"Run the tests"`,
		"future_field": `{"a":[1,2]}`,
	}
	for k, want := range echoed {
		raw, ok := hso.UpdatedInput[k]
		if !ok {
			t.Errorf("updatedInput dropped %q", k)
			continue
		}
		if string(raw) != want {
			t.Errorf("updatedInput[%q] = %s, want byte-identical %s", k, raw, want)
		}
	}
	var cmd string
	if err := json.Unmarshal(hso.UpdatedInput["command"], &cmd); err != nil {
		t.Fatalf("command: %v", err)
	}
	if want := "rundiff -- go test ./..."; cmd != want {
		t.Errorf("command=%q want %q", cmd, want)
	}
	if len(hso.UpdatedInput) != 4 {
		t.Errorf("updatedInput has %d keys, want 4 (the input's, no more)", len(hso.UpdatedInput))
	}
}

// The single most important assertion in this package.
//
// permissionDecision:"allow" would bypass the user's permission system, and
// rundiff execs whatever argv it is handed — a hook that both chooses the command
// and approves it is a hole shaped exactly like Bash(*). updatedInput ALONE is
// honored; permissions are re-checked against the rewritten string. This asserts
// on the MARSHALLED BYTES, not on the type, because bytes are what Claude Code
// actually reads.
func TestRewrite_neverReturnsAPermissionDecision(t *testing.T) {
	for _, c := range rewriteCases {
		body, err := json.Marshal(map[string]any{"command": c.in, "timeout": 600000})
		if err != nil {
			t.Fatal(err)
		}
		got := Rewrite(preToolUse(string(body)), Options{})
		if got == nil {
			t.Fatalf("%s: nil response", c.name)
		}
		for _, forbidden := range []string{"permissionDecision", "permission_decision", "permissionDecisionReason"} {
			if bytes.Contains(bytes.ToLower(got), bytes.ToLower([]byte(forbidden))) {
				t.Fatalf("%s: response carries %q — this bypasses the user's permission system:\n%s",
					c.name, forbidden, got)
			}
		}
	}
}

// The hook's analogue of the adapter's TestExtract_neverFalseFixed: a
// brute-force sweep asserting the bias holds where it is most likely to break.
// Inject every shell metacharacter at every token position of every target
// command — as a suffix, as a prefix, and as the whole token — and require ZERO
// rewrites. A single escape here is a command an agent typed being silently
// replaced by a different one.
func TestRewrite_neverWrapsWhatItCannotExpress(t *testing.T) {
	// Deliberately NOT a plain space: a space is a legal separator, so injecting
	// one merely re-splits the tokens into another valid command — a rewrite there
	// is correct, not a bug. Every byte below is one a shell would ACT on, or one
	// we refuse to re-emit unquoted.
	metas := []string{
		`"`, `'`, `$`, "`", `\`, `|`, `&`, `;`, `<`, `>`, `(`, `)`,
		`{`, `}`, `[`, `]`, `*`, `?`, `!`, `#`, `~`, `%`, `^`,
		"\n", "\r", "\x00", "\v", "\f", "\xff", "\u00a0",
	}

	// One representative command per target, with a trailing argument so there is
	// always an argument position to poison as well as command positions.
	var commands [][]string
	for _, tg := range Targets() {
		commands = append(commands, append(slices.Clone(tg.Prefix), "."))
	}

	tried, rewrote := 0, 0
	check := func(cmd string) {
		tried++
		if got, _, ok := Command(cmd, Options{}); ok {
			rewrote++
			if rewrote <= 10 { // don't drown the log
				t.Errorf("rewrote a command carrying shell syntax:\n  in:  %q\n  out: %q", cmd, got)
			}
		}
		// The event path must refuse in lockstep: no rewrite, no response.
		body, err := json.Marshal(map[string]string{"command": cmd})
		if err != nil {
			t.Fatal(err)
		}
		if resp := Rewrite(preToolUse(string(body)), Options{}); resp != nil {
			t.Errorf("Rewrite() responded to a command carrying shell syntax: %q\n%s", cmd, resp)
		}
	}

	for _, toks := range commands {
		for i := range toks {
			for _, m := range metas {
				for _, poisoned := range []string{toks[i] + m, m + toks[i], m} {
					mutated := slices.Clone(toks)
					mutated[i] = poisoned
					cmd := strings.Join(mutated, " ")
					check(cmd)
					// The same sweep behind the cd carve-out: the tail is gated
					// AFTER the prefix is peeled, so nothing may ride in behind
					// the `&&`.
					check("cd /repo && " + cmd)
				}
				// And the carve-out's own directory token, which is matched by a
				// regexp on the RAW string — the one place a metacharacter is
				// looked at before the byte gate sees it.
				check("cd /re" + m + "po && " + strings.Join(toks, " "))
			}
		}
	}

	if rewrote > 0 {
		t.Fatalf("%d/%d poisoned commands were rewritten; the bias is broken", rewrote, tried)
	}
	t.Logf("swept %d poisoned commands, 0 rewrites", tried)
}

// A rewritten command must round-trip: the response's command is exactly what
// Command() produced, and feeding the response's command back through the hook
// refuses (no `rundiff -- rundiff -- …`).
func TestRewrite_idempotent(t *testing.T) {
	first := Rewrite(preToolUse(`{"command":"go test ./..."}`), Options{})
	if first == nil {
		t.Fatal("nil response")
	}
	var resp response
	if err := json.Unmarshal(first, &resp); err != nil {
		t.Fatal(err)
	}
	var cmd string
	if err := json.Unmarshal(resp.HookSpecificOutput.UpdatedInput["command"], &cmd); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		t.Fatal(err)
	}
	if second := Rewrite(preToolUse(string(body)), Options{}); second != nil {
		t.Fatalf("re-wrapped an already-wrapped command:\n%s", second)
	}
}

// The `cd <dir> && ` prefix survives into the response's command unescaped —
// the default JSON HTML escaping would render its `&&` as a pair of &
// escapes, which decodes the same but reads like a bug to anyone debugging the
// hook.
func TestRewrite_cdPrefixIsNotEscaped(t *testing.T) {
	got := Rewrite(preToolUse(`{"command":"cd /repo/web && npx vitest run"}`), Options{})
	if got == nil {
		t.Fatal("nil response")
	}
	if !bytes.Contains(got, []byte(`cd /repo/web && rundiff -- npx vitest run`)) {
		t.Errorf("cd prefix not carried verbatim:\n%s", got)
	}
}

// EventCommand backs the CLI's "why was my command left alone?" debug line, and
// its whole job is to make that line impossible to get wrong: a non-empty result
// must guarantee Command() has a real, non-empty reason for the non-rewrite.
func TestEventCommand(t *testing.T) {
	cases := []struct {
		name  string
		event string
		want  string
	}{
		{"a command it acts on", `{"command":"npm test"}`, "npm test"},
		{"a command it rewrites", `{"command":"go test ./..."}`, "go test ./..."},
		{"no command", `{"timeout":600000}`, ""},
		{"command is a number", `{"command":42}`, ""},
		// Backgrounded: the event carries a command, but not one the hook acts on —
		// so it is "" and the CLI says "not an event with a command" rather than
		// inventing a reason Command() never gave.
		{"backgrounded", `{"command":"go test ./...","run_in_background":true}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EventCommand(preToolUse(c.event)); got != c.want {
				t.Errorf("EventCommand() = %q want %q", got, c.want)
			}
		})
	}

	for _, ev := range []string{
		`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"command":"go test ./..."}}`,
		`not json`,
		``,
	} {
		if got := EventCommand([]byte(ev)); got != "" {
			t.Errorf("EventCommand(%q) = %q, want \"\" (not an event the hook acts on)", ev, got)
		}
	}

	// The invariant, over every case in both tables: if EventCommand yields a
	// command and Rewrite declined, Command must own that refusal and name it.
	for _, c := range append(append([]struct{ name, in string }{}, toNamed(rewriteCases)...), toNamedRefuse(refuseCases)...) {
		body, err := json.Marshal(map[string]string{"command": c.in})
		if err != nil {
			t.Fatal(err)
		}
		ev := preToolUse(string(body))
		cmd := EventCommand(ev)
		if cmd == "" {
			t.Errorf("%s: EventCommand returned \"\" for a Bash event carrying a command", c.name)
			continue
		}
		if cmd != c.in {
			t.Errorf("%s: EventCommand = %q want %q", c.name, cmd, c.in)
		}
		_, reason, ok := Command(cmd, Options{})
		if got := Rewrite(ev, Options{}); (got != nil) != ok {
			t.Errorf("%s: Rewrite and Command disagree (rewrote=%v, ok=%v)", c.name, got != nil, ok)
		}
		if !ok && reason == "" {
			t.Errorf("%s: refused with an empty reason — the debug line would say nothing", c.name)
		}
	}
}

func toNamed(cs []struct{ name, in, want string }) []struct{ name, in string } {
	out := make([]struct{ name, in string }, len(cs))
	for i, c := range cs {
		out[i] = struct{ name, in string }{c.name, c.in}
	}
	return out
}

func toNamedRefuse(cs []struct{ name, in, reason string }) []struct{ name, in string } {
	out := make([]struct{ name, in string }, 0, len(cs))
	for _, c := range cs {
		if c.in == "" {
			continue // the empty command is not "a Bash event carrying a command"
		}
		out = append(out, struct{ name, in string }{c.name, c.in})
	}
	return out
}

// additionalContext is factual and declarative — it must not read as an
// instruction, or a prompt-injection defense will surface it to the user instead
// of the model consuming it as context. It must also actually say the things an
// agent needs in order to read a delta correctly.
func TestAdditionalContext(t *testing.T) {
	ctx := AdditionalContext()
	if ctx == "" {
		t.Fatal("empty")
	}
	for _, must := range []string{"rundiff", "JSON", "null", "CHANGED", "empty body", "--full", "RUNDIFF_HOOK=0"} {
		if !strings.Contains(ctx, must) {
			t.Errorf("additionalContext does not convey %q:\n%s", must, ctx)
		}
	}
	if strings.Contains(ctx, "\n") {
		t.Error("additionalContext must be one paragraph (no newlines)")
	}
}
