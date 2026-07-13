package hook

import (
	"strings"
	"testing"
)

// rewriteCases are the commands the hook is FOR. They double as the fuzz corpus
// and as the input to TestSplit_matchesRealShell, which is the only test that
// checks the no-quoter claim against an actual shell.
var rewriteCases = []struct {
	name string
	in   string
	want string
}{
	{"go test", "go test ./...", "rundiff -- go test ./..."},
	{"go test -run", "go test ./... -run=TestX", "rundiff -- go test ./... -run=TestX"},
	{"go test -count", "go test -count=1 ./internal/...", "rundiff -- go test -count=1 ./internal/..."},
	{"pytest bare", "pytest", "rundiff -- pytest"},
	{"python3 -m pytest", "python3 -m pytest tests/", "rundiff -- python3 -m pytest tests/"},
	{"npx vitest run", "npx vitest run", "rundiff -- npx vitest run"},
	{"pnpm exec vitest run", "pnpm exec vitest run src/a.test.ts", "rundiff -- pnpm exec vitest run src/a.test.ts"},
	{"npx tsc", "npx tsc --noEmit", "rundiff -- npx tsc --noEmit"},
	{"eslint", "eslint . --ext .ts,.tsx", "rundiff -- eslint . --ext .ts,.tsx"},
	{"cargo test", "cargo test", "rundiff -- cargo test"},
	{"cd carve-out", "cd /repo/web && npx vitest run", "cd /repo/web && rundiff -- npx vitest run"},
	// Runs of spaces collapse: one command, one cache key, whatever the typing.
	{"space runs canonicalize", "go   test   ./...", "rundiff -- go test ./..."},
}

func TestCommand_rewrites(t *testing.T) {
	for _, c := range rewriteCases {
		t.Run(c.name, func(t *testing.T) {
			got, reason, ok := Command(c.in, Options{})
			if !ok {
				t.Fatalf("Command(%q) refused: %s", c.in, reason)
			}
			if reason != "" {
				t.Errorf("ok with a reason: %q", reason)
			}
			if got != c.want {
				t.Errorf("Command(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

// refuseCases: every one is a command the hook must leave EXACTLY as typed. The
// reason is asserted, not just the refusal — a case refusing for the wrong reason
// is a case that would start rewriting the day that reason moved.
var refuseCases = []struct {
	name   string
	in     string
	reason string
}{
	// Not a target. A script runner hides its body from argv; a build makes no
	// claim; bare vitest is watch mode.
	{"npm test", "npm test", "not-a-target"},
	{"pnpm test", "pnpm test", "not-a-target"},
	{"npm run test", "npm run test", "not-a-target"},
	{"go build", "go build ./...", "not-a-target"},
	{"git status", "git status", "not-a-target"},
	{"bare vitest is watch mode", "vitest", "not-a-target"},

	// Shell syntax: refused by the byte gate, before it can mean anything.
	// rundiff REPLACES stdout, so a consumed stdout is a broken consumer.
	{"pipe", "go test ./... | tee log", "unsplittable"},
	{"and-chain", "go test ./... && echo done", "unsplittable"},
	{"redirect", "go test ./... > out.txt", "unsplittable"},
	{"command substitution", "go test $(pkgs)", "unsplittable"},
	{"double quotes", `pytest -k "a or b"`, "unsplittable"},
	{"quoted -t", `vitest run -t "my test"`, "unsplittable"},
	{"single quotes + alternation", "go test ./... -run 'TestA|TestB'", "unsplittable"},
	{"glob", "eslint src/**/*.ts", "unsplittable"},
	{"non-ASCII path", "pytest tests/日本語/", "unsplittable"},
	{"empty", "", "unsplittable"},

	// Env prefix — the token itself is the refusal, no variable is ever read.
	{"env prefix", "GOFLAGS=-run=X go test ./...", "env-prefix"},

	// `env` the PROGRAM displaces the tool out of the command position.
	{"env(1) launcher", "env GOFLAGS=x go test ./...", "not-a-target"},

	// Idempotence, by basename, at any path.
	{"already wrapped", "rundiff -- go test ./...", "already-wrapped"},
	{"already wrapped, absolute", "/opt/homebrew/bin/rundiff -- go test ./...", "already-wrapped"},
	{"already wrapped, relative + flag", "./rundiff --json -- go test ./...", "already-wrapped"},

	// Refused flags: watch/interactive (hangs), machine output (stdout is the
	// point), not-a-test-run (nothing to claim).
	{"jest --watch", "jest --watch", "refused-flag:--watch"},
	{"vitest --ui", "vitest run --ui", "refused-flag:--ui"},
	{"go -json", "go test -json ./...", "refused-flag:-json"},
	{"eslint -f", "eslint . -f json", "refused-flag:-f"},
	{"tsc --watch", "tsc --watch", "refused-flag:--watch"},
	{"pytest --collect-only", "pytest --collect-only", "refused-flag:--collect-only"},
	{"go --help", "go test --help", "refused-flag:--help"},
	// Go's flag package takes one OR two dashes for the same flag. `-bench=.`
	// prints timings that differ every run — a delta of pure noise — and `-fuzz`
	// runs until it is killed: the watcher hang, reached by a flag instead of by
	// a tool.
	{"go -bench glued", "go test -bench=. ./...", "refused-flag:-bench=."},
	{"go -bench separate", "go test -bench . ./...", "refused-flag:-bench"},
	{"go -fuzz", "go test -fuzz=FuzzDiff ./internal/delta", "refused-flag:-fuzz=FuzzDiff"},

	// The cd carve-out is a regexp on the RAW string, and it is narrow: a space
	// inside the directory token must not match, and the tail is byte-gated after,
	// so a second shell operator cannot ride in behind the `&&`.
	{"space in the cd token", "cd /a b && go test ./...", "unsplittable"},
	{"cd + pipe in the tail", "cd /repo && go test ./... | tee", "unsplittable"},
}

func TestCommand_refuses(t *testing.T) {
	for _, c := range refuseCases {
		t.Run(c.name, func(t *testing.T) {
			got, reason, ok := Command(c.in, Options{})
			if ok {
				t.Fatalf("Command(%q) rewrote to %q, want refusal (%s)", c.in, got, c.reason)
			}
			if got != "" {
				t.Errorf("refusal returned a rewrite: %q", got)
			}
			if reason != c.reason {
				t.Errorf("Command(%q) reason=%q want %q", c.in, reason, c.reason)
			}
		})
	}
}

// The escape hatch, named so it can never be deleted as "just another env
// prefix". `RUNDIFF_HOOK=0 <cmd>` is the documented way to run anything
// unwrapped, and it works BECAUSE an assignment in the command position is
// refused outright — no variable is read, so the hatch cannot be broken by an
// environment the hook binary cannot see.
func TestCommand_escapeHatch(t *testing.T) {
	got, reason, ok := Command("RUNDIFF_HOOK=0 go test ./...", Options{})
	if ok {
		t.Fatalf("the escape hatch was rewritten to %q", got)
	}
	if reason != "env-prefix" {
		t.Errorf("reason=%q want env-prefix", reason)
	}
}

// The length gate is on bytes, and it fires before anything is parsed.
func TestCommand_tooLong(t *testing.T) {
	long := "go test " + strings.Repeat("a", 4097-len("go test "))
	if len(long) != 4097 {
		t.Fatalf("test setup: len=%d want 4097", len(long))
	}
	if _, reason, ok := Command(long, Options{}); ok || reason != "unsplittable" {
		t.Errorf("4097 safe bytes: ok=%v reason=%q want refusal/unsplittable", ok, reason)
	}
	// One byte shorter, same alphabet: accepted. The gate is length, not luck.
	if _, _, ok := Command(long[:4096], Options{}); !ok {
		t.Error("4096 safe bytes refused; the cap is off by one")
	}
}

func TestCommand_bin(t *testing.T) {
	got, _, ok := Command("go test ./...", Options{Bin: "/opt/homebrew/bin/rundiff"})
	if !ok {
		t.Fatal("refused")
	}
	if want := "/opt/homebrew/bin/rundiff -- go test ./..."; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	// And it is still idempotent under a DIFFERENT Bin than it was wrapped with:
	// the already-wrapped check is by basename, not by the configured path.
	if _, reason, ok := Command(got, Options{Bin: "rundiff"}); ok || reason != "already-wrapped" {
		t.Errorf("cross-Bin idempotence: ok=%v reason=%q", ok, reason)
	}
}

func TestTargets(t *testing.T) {
	got := Targets()
	if len(got) != 18 {
		t.Errorf("len(Targets())=%d want 18", len(got))
	}
	// The Tool labels must be exactly the internal/adapter parser names. hook does
	// not import adapter (both are leaves), so this asserts the shape locally; the
	// set-equality drift alarm against adapter.Tools() lives in internal/cli.
	wantTools := map[string]bool{
		"go-test": true, "pytest": true, "jest": true, "vitest": true,
		"cargo-test": true, "tsc": true, "eslint": true,
	}
	seen := map[string]bool{}
	for _, tg := range got {
		if len(tg.Prefix) == 0 {
			t.Errorf("target %+v has an empty prefix", tg)
		}
		if !wantTools[tg.Tool] {
			t.Errorf("target %v: tool %q is not an adapter parser name", tg.Prefix, tg.Tool)
		}
		seen[tg.Tool] = true
		// Every prefix must round-trip through the gate it will be matched by,
		// or it could never fire.
		if _, ok := Split(strings.Join(tg.Prefix, " ")); !ok {
			t.Errorf("target %v does not survive its own byte gate", tg.Prefix)
		}
	}
	if len(seen) != len(wantTools) {
		t.Errorf("covered %d tools, want %d", len(seen), len(wantTools))
	}
}

// Targets() hands out a deep copy: the target list is this package's only shared
// data, and a caller must not be able to widen what gets rewritten by mutating
// the slice it was given.
func TestTargets_isACopy(t *testing.T) {
	got := Targets()
	got[0].Prefix[0] = "rm"
	got[0].Tool = "pwned"
	fresh := Targets()
	if fresh[0].Prefix[0] == "rm" || fresh[0].Tool == "pwned" {
		t.Fatal("Targets() aliases package state; a caller can widen the rewrite set")
	}
	if !matchTarget([]string{"go", "test", "."}) {
		t.Fatal("mutating the returned slice changed what matches")
	}
}

func TestSplit(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		ok   bool
	}{
		{"go test ./...", []string{"go", "test", "./..."}, true},
		{"go\ttest\t./...", []string{"go", "test", "./..."}, true},
		{"  go   test  ", []string{"go", "test"}, true},
		{"eslint . --ext .ts,.tsx", []string{"eslint", ".", "--ext", ".ts,.tsx"}, true},
		{"a@b:c+d=e", []string{"a@b:c+d=e"}, true},
		{"", nil, false},
		{"   ", nil, false},
		{"go test\nrm -rf /", nil, false},
		{"go test\rfoo", nil, false},
		{"go test\x00foo", nil, false},
		// \v, \f and U+00A0 are the split-first trap: strings.Fields treats them
		// as whitespace and a real shell does not. Gating before splitting is what
		// makes them a refusal instead of a silent re-wording of the command.
		{"go test\vfoo", nil, false},
		{"go test\ffoo", nil, false},
		{"go test\u00a0foo", nil, false}, // NBSP: IsSpace, but not to a shell
		{"go test ~/x", nil, false},
		{"go test %s", nil, false},
		{"go test ^x", nil, false},
		{"go test !x", nil, false},
		{"go test #x", nil, false},
	}
	for _, c := range cases {
		got, ok := Split(c.in)
		if ok != c.ok {
			t.Errorf("Split(%q) ok=%v want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("Split(%q) = %q want %q", c.in, got, c.want)
		}
	}
}
