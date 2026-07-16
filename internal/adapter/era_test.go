package adapter

import (
	"reflect"
	"testing"
)

// Cross-era regression. The parsers fingerprint output, not versions, so a
// format that drifts between tool releases must either keep parsing or abstain
// — never lie. These captures are ADDITIONAL eras of each tool (older or
// newer than the fail.out/pass.out era), taken from real runs, and they pin
// exactly that: the same claim where the format is compatible, and honest
// silence where it is not.
//
// Provenance is in each tool's testdata/captures/<tool>/VERSIONS. The current
// era lives in fail.out/pass.out; other eras in v<N>-fail.out/v<N>-pass.out.
// Regenerate candidates with scripts/gen-adapter-fixtures.sh (e.g.
// `sh scripts/gen-adapter-fixtures.sh vitest@1.6.1` to re-take an old era).
func TestExtract_acrossEras(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		era     string
		argv    []string
		failing []string // failing identities on the failing run
		fixed   []string // fixed identities on the fail→pass transition; nil = pair withheld
	}{
		{
			// vitest 1.x drifted from 3.x: the file-summary line pads the path
			// with two spaces before `(N test)`, and an all-pass file prints no
			// per-test `✓ name` line. The parser's `\s+\(` and count-paren anchor
			// ride over both.
			name: "vitest 1.x", tool: "vitest", era: "v1",
			argv:    []string{"vitest", "run"},
			failing: []string{"src/math.test.ts"},
			fixed:   []string{"src/math.test.ts"},
		},
		{
			// eslint 8 (legacy .eslintrc) prints the same stylish output as 9
			// (flat config): the file heading, the severity rows, and the
			// `✖ N problems` sentinel are unchanged. A warnings-only re-run
			// (exit 0, zero errors) is the global clean proof that mints fixed.
			name: "eslint 8", tool: "eslint", era: "v8",
			argv:    []string{"eslint", "."},
			failing: []string{"/home/dev/fixture/src/bad.js"},
			fixed:   []string{"/home/dev/fixture/src/bad.js"},
		},
		{
			// pytest 7's progress line, FAILURES banner and short-summary are the
			// same shape 9 prints; the file-level identity survives unchanged.
			name: "pytest 7", tool: "pytest", era: "v7",
			argv:    []string{"pytest"},
			failing: []string{"test_math.py"},
			fixed:   []string{"test_math.py"},
		},
		{
			// jest 30 is the drift that MATTERS: on an all-pass run without a TTY
			// (which is exactly how rundiff's runner invokes it) jest 30 prints
			// NO per-file `PASS <file>` lines — only the summary — where jest 29
			// prints them. jest has no global clean proof (it is not
			// silentWhenClean), so the previously-failing file carries no pass
			// evidence and the pair is WITHHELD. The failing set and the line
			// diff still stand; only fixed/new go silent. Safe, not a lie — and
			// the reason `fixed` for jest is unavailable through the hook.
			name: "jest 30 (no PASS lines without a TTY)", tool: "jest", era: "v30",
			argv:    []string{"jest"},
			failing: []string{"src/math.test.js"},
			fixed:   nil,
		},
		{
			// vitest 4 repeats jest 30's drift: an all-pass run prints NO
			// per-file lines at all — only the `Test Files N passed` summary —
			// where 3.x prints `✓ file (N tests)`. vitest has no global clean
			// proof (not silentWhenClean), so the previously-failing file
			// carries no pass evidence and the pair is WITHHELD. The failing
			// set on a red run still parses; only fixed/new go silent.
			name: "vitest 4 (no per-file lines on all-pass)", tool: "vitest", era: "v4",
			argv:    []string{"vitest", "run"},
			failing: []string{"vtests/math.test.js"},
			fixed:   nil,
		},
		{
			// eslint 10 keeps the stylish shape: file heading, severity rows,
			// `✖ N problems` sentinel; a clean run is still zero bytes + exit 0
			// — the silentWhenClean proof that mints fixed.
			name: "eslint 10", tool: "eslint", era: "v10",
			argv:    []string{"eslint", "src"},
			failing: []string{"/home/dev/fixture/src/bad.js"},
			fixed:   []string{"/home/dev/fixture/src/bad.js"},
		},
		{
			// tsc 7 (the native compiler) keeps the plain `file(line,col):
			// error TSnnnn:` shape, and a clean run stays zero bytes + exit 0.
			name: "tsc 7", tool: "tsc", era: "v7",
			argv:    []string{"tsc", "--noEmit", "ts/a.ts", "ts/b.ts"},
			failing: []string{"ts/a.ts", "ts/b.ts"},
			fixed:   []string{"ts/a.ts", "ts/b.ts"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fail := loadCapture(t, c.tool, c.era+"-fail")
			pass := loadCapture(t, c.tool, c.era+"-pass")

			cf := Extract(c.argv, nil, nil, fail, "")
			if cf == nil || cf.Tool != c.tool {
				t.Fatalf("failing run: got %+v, want a %s claim", cf, c.tool)
			}
			if !reflect.DeepEqual(cf.Failing, c.failing) {
				t.Errorf("failing = %v, want %v", cf.Failing, c.failing)
			}

			cx := Extract(c.argv, nil, &fail, pass, "")
			if cx == nil {
				t.Fatalf("fail→pass: nil claim, want a %s claim (failing set must survive)", c.tool)
			}
			if !reflect.DeepEqual(cx.Fixed, c.fixed) {
				t.Errorf("fixed = %v, want %v", cx.Fixed, c.fixed)
			}
		})
	}
}
