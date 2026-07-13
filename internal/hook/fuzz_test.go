package hook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// fuzzBins are the Options a fuzz case may run under: the default, the explicit
// default, and an absolute install path (the Homebrew case).
var fuzzBins = []string{"", "rundiff", "/opt/homebrew/bin/rundiff"}

// FuzzCommand hunts for a REWRITE the model cannot justify, not merely a panic.
// Every accepted string must satisfy the whole contract:
//
//   - the output is exactly prefix + bin + " -- " + strings.Join(argv, " ") —
//     the CLI's exec.Command is entitled to assume nothing else was inserted;
//   - every byte of it is inert in sh word context (the money property), so the
//     shell that runs it re-splits it into the argv we intended;
//   - it is idempotent: wrapping a wrapped command is refused, so a retried or
//     nested hook cannot build `rundiff -- rundiff -- go test`;
//   - a refusal carries a reason and no rewrite, and an acceptance carries a
//     rewrite and no reason.
func FuzzCommand(f *testing.F) {
	for _, c := range rewriteCases {
		f.Add(c.in, byte(0))
		f.Add(c.in, byte(2))
	}
	for _, c := range refuseCases {
		f.Add(c.in, byte(0))
	}
	for _, s := range []string{
		"cd / && go test", "cd  && go test", "cd /a && cd /b && go test",
		"go test =", "= go test", "go\ttest", "rundiff", "/rundiff -- x",
		strings.Repeat("go test ", 600),
	} {
		f.Add(s, byte(1))
	}

	f.Fuzz(func(t *testing.T, s string, binIdx byte) {
		opt := Options{Bin: fuzzBins[int(binIdx)%len(fuzzBins)]}
		bin := opt.Bin
		if bin == "" {
			bin = binName
		}

		got, reason, ok := Command(s, opt)

		if !ok {
			if got != "" {
				t.Fatalf("refusal returned a rewrite: %q", got)
			}
			if reason == "" {
				t.Fatalf("refusal with no reason for %q", s)
			}
			return
		}
		if reason != "" {
			t.Fatalf("acceptance carried a reason %q for %q", reason, s)
		}

		// 1. Exact shape. Re-derive it independently of Command's own code path.
		prefix, tail := splitCDPrefix(s)
		argv, split := Split(tail)
		if !split {
			t.Fatalf("accepted %q whose tail does not split", s)
		}
		want := prefix + bin + " -- " + strings.Join(argv, " ")
		if got != want {
			t.Fatalf("Command(%q)\n got %q\nwant %q", s, got, want)
		}

		// 2. The money property. Everything after the (regexp-shaped) cd prefix
		// must be safe bytes and spaces — no quoter needed, ever.
		if prefix != "" && !reCDPrefix.MatchString(s) {
			t.Fatalf("a prefix appeared without the cd regexp matching: %q", s)
		}
		rest := strings.TrimPrefix(got, prefix)
		for i := 0; i < len(rest); i++ {
			if b := rest[i]; !safeByte(b) && b != ' ' {
				t.Fatalf("output byte %q at %d is not inert in sh word context: %q", b, i, got)
			}
		}

		// 3. Idempotence. The one exception is length: wrapping ADDS bytes, so a
		// command just under the cap can push its own rewrite over it — and being
		// refused for being too long is still being refused.
		_, reason2, ok2 := Command(got, opt)
		if ok2 {
			t.Fatalf("Command is not idempotent: %q → %q → rewritten again", s, got)
		}
		if reason2 != "already-wrapped" && reason2 != "unsplittable" {
			t.Fatalf("re-running Command(%q) refused with %q, want already-wrapped", got, reason2)
		}
		if reason2 == "unsplittable" && len(got) <= maxCommandBytes {
			t.Fatalf("Command(%q) is within the cap yet unsplittable", got)
		}
	})
}

// FuzzRewrite: on ARBITRARY bytes the event path must produce either nil or one
// valid, single-line JSON object that never carries a permissionDecision — the
// field whose presence would hand rundiff's arbitrary-argv exec a blanket
// approval. Whatever the input, the response's command must be exactly what
// Command() independently decides for it.
func FuzzRewrite(f *testing.F) {
	for _, c := range rewriteCases {
		body, err := json.Marshal(map[string]any{"command": c.in, "timeout": 600000})
		if err != nil {
			f.Fatal(err)
		}
		f.Add(preToolUse(string(body)), byte(0))
	}
	for _, c := range refuseCases {
		body, err := json.Marshal(map[string]string{"command": c.in})
		if err != nil {
			f.Fatal(err)
		}
		f.Add(preToolUse(string(body)), byte(0))
	}
	for _, s := range []string{
		``, `{}`, `null`, `[]`, `{"hook_event_name":"PreToolUse"}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./...","run_in_background":true}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":["go","test"]}}`,
	} {
		f.Add([]byte(s), byte(2))
	}

	f.Fuzz(func(t *testing.T, event []byte, binIdx byte) {
		opt := Options{Bin: fuzzBins[int(binIdx)%len(fuzzBins)]}
		got := Rewrite(event, opt)
		if got == nil {
			return
		}

		if !json.Valid(got) {
			t.Fatalf("invalid JSON:\n%s", got)
		}
		if bytes.ContainsAny(got, "\n\r") {
			t.Fatalf("response is not a single line:\n%q", got)
		}
		if bytes.Contains(bytes.ToLower(got), []byte("permissiondecision")) {
			t.Fatalf("response carries a permissionDecision:\n%s", got)
		}

		var resp struct {
			HookSpecificOutput *hookSpecificOutput `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(got, &resp); err != nil {
			t.Fatalf("unmarshal: %v\n%s", err, got)
		}
		if resp.HookSpecificOutput == nil {
			t.Fatalf("no hookSpecificOutput:\n%s", got)
		}
		hso := resp.HookSpecificOutput
		if hso.HookEventName != "PreToolUse" {
			t.Fatalf("hookEventName=%q", hso.HookEventName)
		}
		if hso.AdditionalContext == "" {
			t.Fatal("empty additionalContext")
		}

		// The response's command is exactly what Command() decides, and it is a
		// command Command() would then refuse to wrap again.
		var out string
		if err := json.Unmarshal(hso.UpdatedInput["command"], &out); err != nil {
			t.Fatalf("updatedInput.command is not a string: %v", err)
		}
		var ev event2
		if err := json.Unmarshal(event, &ev); err != nil {
			t.Fatalf("event decoded once but not twice: %v", err)
		}
		var in string
		if err := json.Unmarshal(ev.ToolInput["command"], &in); err != nil {
			t.Fatalf("input command is not a string: %v", err)
		}
		want, _, ok := Command(in, opt)
		if !ok || out != want {
			t.Fatalf("responded %q for input %q, but Command() says (%q, ok=%v)", out, in, want, ok)
		}
		if _, _, ok := Command(out, opt); ok {
			t.Fatalf("the response's command would be wrapped again: %q", out)
		}

		// Every input field survives, byte for byte, except command.
		for k, v := range ev.ToolInput {
			if k == "command" {
				continue
			}
			got, present := hso.UpdatedInput[k]
			if !present {
				t.Fatalf("updatedInput dropped %q", k)
			}
			if !bytes.Equal(compact(t, v), compact(t, got)) {
				t.Fatalf("updatedInput[%q] = %s, want %s", k, got, v)
			}
		}
	})
}

// event2 mirrors the unexported event type for the fuzz test's own decode (it
// must not reuse the code under test to derive its expectations).
type event2 struct {
	ToolInput map[string]json.RawMessage `json:"tool_input"`
}

func compact(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatalf("compact %s: %v", raw, err)
	}
	return buf.Bytes()
}

// FuzzSplit: the gate is total and its guarantee is absolute. Anything it
// accepts contains ONLY inert bytes, joins back to something it accepts
// identically (the join is a fixed point), and has at least one word.
func FuzzSplit(f *testing.F) {
	for _, s := range []string{
		"go test ./...", "", "   ", "a\tb", "a|b", "a\x00b", "日本語",
		strings.Repeat("a", maxCommandBytes), strings.Repeat("a", maxCommandBytes+1),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		argv, ok := Split(s)
		if !ok {
			if argv != nil {
				t.Fatalf("refusal returned argv %q", argv)
			}
			return
		}
		if len(argv) == 0 {
			t.Fatal("accepted with no words")
		}
		joined := strings.Join(argv, " ")
		for i := 0; i < len(joined); i++ {
			if b := joined[i]; !safeByte(b) && b != ' ' {
				t.Fatalf("accepted a non-inert byte %q in %q", b, s)
			}
		}
		// Fixed point: re-splitting the join yields the same words. This is the
		// property the whole rewrite rests on.
		again, ok2 := Split(joined)
		if !ok2 || strings.Join(again, "\x00") != strings.Join(argv, "\x00") {
			t.Fatalf("join is not a fixed point: %q → %q → %q (ok=%v)", s, argv, again, ok2)
		}
	})
}
