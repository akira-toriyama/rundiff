package hook

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (regenerate: go test ./internal/hook -update)", err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is stale (regenerate: go test ./internal/hook -update)\n--- got ---\n%s\n--- want ---\n%s", p, got, want)
	}
}

func TestPrint(t *testing.T) {
	golden(t, "print.txt", Print(Options{}, false))
	golden(t, "print.json", Print(Options{}, true))
}

// The JSON form is config a user merges into settings.json: it must parse, and
// it must say exactly what we think it says.
func TestPrint_json(t *testing.T) {
	raw := Print(Options{}, true)
	if !json.Valid(raw) {
		t.Fatalf("not valid JSON:\n%s", raw)
	}
	var doc settings
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}

	// One matcher, one handler — a second handler would run the hook twice.
	if len(doc.Hooks.PreToolUse) != 1 {
		t.Fatalf("%d PreToolUse matchers, want 1", len(doc.Hooks.PreToolUse))
	}
	m := doc.Hooks.PreToolUse[0]
	if m.Matcher != "Bash" {
		t.Errorf("matcher=%q want Bash", m.Matcher)
	}
	if len(m.Hooks) != 1 {
		t.Fatalf("%d handlers, want 1", len(m.Hooks))
	}
	h := m.Hooks[0]

	// A KNOWN posix shell in exec form: a login shell would source the user's rc
	// files, and anything they print lands on the hook's stdout, where Claude Code
	// expects JSON and nothing else.
	if h.Command != "/bin/sh" {
		t.Errorf("command=%q want /bin/sh", h.Command)
	}
	if h.Type != "command" {
		t.Errorf("type=%q want command", h.Type)
	}
	if len(h.Args) != 2 || h.Args[0] != "-c" {
		t.Fatalf("args=%q want [-c <script>]", h.Args)
	}
	// The guard: an uninstalled rundiff must be a silent no-op, not an error on
	// every single Bash call the user makes.
	if !strings.Contains(h.Args[1], "command -v rundiff") {
		t.Errorf("args[1] has no `command -v rundiff` guard: %q", h.Args[1])
	}
	// No `exec`, so `|| exit 0` stays reachable for a rundiff too old to have the
	// subcommand — an upgrade-lagged install degrades to "no rewrite".
	if strings.Contains(h.Args[1], "exec ") {
		t.Errorf("args[1] execs, which makes `|| exit 0` unreachable: %q", h.Args[1])
	}
	if !strings.Contains(h.Args[1], "|| exit 0") {
		t.Errorf("args[1] has no `|| exit 0` fallback: %q", h.Args[1])
	}
	if h.Timeout != 5 {
		t.Errorf("timeout=%d want 5", h.Timeout)
	}

	// Exactly one permission entry per target: the allowed set can never be wider
	// than what the hook can actually produce.
	if len(doc.Permissions.Allow) != len(Targets()) {
		t.Fatalf("%d permission entries for %d targets — they must be 1:1",
			len(doc.Permissions.Allow), len(Targets()))
	}
	want := map[string]bool{}
	for _, tg := range Targets() {
		want["Bash(rundiff -- "+strings.Join(tg.Prefix, " ")+":*)"] = true
	}
	for _, e := range doc.Permissions.Allow {
		if !want[e] {
			t.Errorf("permission entry %q does not correspond to any target", e)
		}
		delete(want, e)
	}
	for e := range want {
		t.Errorf("no permission entry for target %q", e)
	}
}

// The security assertion. `Bash(rundiff:*)` approves rundiff with ANY argv, and
// rundiff execs whatever argv it is handed — that one entry is `Bash(*)` in a
// costume. It must never appear in anything this package prints, in either form,
// including in the human commentary where a reader might copy it.
func TestPrint_neverGrantsBlanketRundiff(t *testing.T) {
	for _, form := range []struct {
		name   string
		asJSON bool
	}{{"human", false}, {"json", true}} {
		for _, opt := range []Options{{}, {Bin: "/opt/homebrew/bin/rundiff"}} {
			out := string(Print(opt, form.asJSON))
			bin := opt.Bin
			if bin == "" {
				bin = binName
			}
			for _, forbidden := range []string{
				`"Bash(` + bin + `:*)"`,
				`"Bash(` + bin + ` *)"`,
				`"Bash(` + bin + `:*)`,
			} {
				if strings.Contains(out, forbidden) {
					t.Errorf("%s form (bin=%q) contains a blanket grant %q — that is Bash(*) in a costume",
						form.name, opt.Bin, forbidden)
				}
			}
		}
	}
}

// An absolute Bin must be substituted EVERYWHERE — the guard, the args, and every
// permission entry. A permission entry naming a different path than the hook
// actually runs would prompt on every command.
func TestPrint_absoluteBin(t *testing.T) {
	const bin = "/opt/homebrew/bin/rundiff"
	raw := Print(Options{Bin: bin}, true)
	var doc settings
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}

	// An absolute path cannot be found with `command -v`; it is tested for
	// executability instead.
	script := doc.Hooks.PreToolUse[0].Hooks[0].Args[1]
	if !strings.Contains(script, "[ -x "+bin+" ]") {
		t.Errorf("guard does not stat the absolute bin: %q", script)
	}
	if strings.Contains(script, "command -v") {
		t.Errorf("guard still uses `command -v` for an absolute path: %q", script)
	}
	if !strings.Contains(script, bin+" hook rewrite") {
		t.Errorf("guard does not invoke the absolute bin: %q", script)
	}

	if len(doc.Permissions.Allow) != len(Targets()) {
		t.Fatalf("%d permission entries for %d targets", len(doc.Permissions.Allow), len(Targets()))
	}
	for _, e := range doc.Permissions.Allow {
		if !strings.HasPrefix(e, "Bash("+bin+" -- ") {
			t.Errorf("permission entry %q does not name the configured bin", e)
		}
	}

	// And the entries must match what Command() would actually produce, or the
	// user would be granting permission for a string the hook never emits.
	got, _, ok := Command("go test ./...", Options{Bin: bin})
	if !ok {
		t.Fatal("refused")
	}
	if !strings.HasPrefix(got, bin+" -- go test") {
		t.Fatalf("rewrite %q is not covered by any printed permission entry", got)
	}
}

// The human form is the JSON form with a commented preamble: a user is being
// asked to grant exec permissions and deserves the reasoning above the thing
// they paste. The JSON must survive verbatim inside it.
func TestPrint_humanFormWrapsTheJSON(t *testing.T) {
	human := string(Print(Options{}, false))
	body := string(Print(Options{}, true))
	if !strings.HasSuffix(human, body) {
		t.Fatal("the human form does not end with the exact JSON form")
	}
	preamble := strings.TrimSuffix(human, body)
	for _, line := range strings.Split(strings.TrimRight(preamble, "\n"), "\n") {
		if !strings.HasPrefix(line, "#") {
			t.Fatalf("preamble line is not a comment: %q", line)
		}
	}
	// The refusals are what users ask about, so they must be explained, not just
	// implied — along with the escape hatches and the never-blanket warning.
	for _, must := range []string{
		"pipe", "redirect", "glob", "quote", "npm test", "watch",
		"RUNDIFF_HOOK=0", "--full",
		"command -v", "exec", "permissionDecision", "REWRITTEN",
		"⚠", "Bash(cd:*)",
	} {
		if !strings.Contains(preamble, must) {
			t.Errorf("the commentary never mentions %q", must)
		}
	}
}
