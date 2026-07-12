package adapter

import (
	"reflect"
	"testing"
)

// Per-tool capture-pair expectations. The invariant sweeps in extract_test.go
// cover the algebra; these pin the concrete identities each tool must yield.
func TestTools_capturePairs(t *testing.T) {
	cases := []struct {
		tool        string
		argv        []string
		prevSc      string
		curSc       string
		wantFailing []string
		wantFixed   []string
		wantNew     []string
		wantNilPair bool
	}{
		{
			tool: "pytest", argv: []string{"pytest"}, prevSc: "fail", curSc: "pass",
			wantFailing: []string{}, wantFixed: []string{"tests/test_math.py"}, wantNew: []string{},
		},
		{
			tool: "pytest", argv: []string{"pytest"}, prevSc: "pass", curSc: "fail",
			wantFailing: []string{"tests/test_math.py"},
			wantFixed:   []string{}, wantNew: []string{"tests/test_math.py"},
		},
		{
			tool: "jest", argv: []string{"jest"}, prevSc: "fail", curSc: "pass",
			wantFailing: []string{}, wantFixed: []string{"__tests__/math.test.js"}, wantNew: []string{},
		},
		{
			tool: "jest", argv: []string{"npx", "jest"}, prevSc: "pass", curSc: "fail",
			wantFailing: []string{"__tests__/math.test.js"},
			wantFixed:   []string{}, wantNew: []string{"__tests__/math.test.js"},
		},
		{
			tool: "vitest", argv: []string{"vitest", "run"}, prevSc: "fail", curSc: "pass",
			wantFailing: []string{}, wantFixed: []string{"vtests/math.test.js"}, wantNew: []string{},
		},
		{
			tool: "vitest", argv: []string{"vitest", "run"}, prevSc: "pass", curSc: "fail",
			wantFailing: []string{"vtests/math.test.js"},
			wantFixed:   []string{}, wantNew: []string{"vtests/math.test.js"},
		},
		{
			tool: "cargo-test", argv: []string{"cargo", "test"}, prevSc: "fail", curSc: "pass",
			wantFailing: []string{}, wantFixed: []string{"tests::test_mul"}, wantNew: []string{},
		},
		{
			tool: "cargo-test", argv: []string{"cargo", "test"}, prevSc: "pass", curSc: "fail",
			wantFailing: []string{"tests::test_mul"},
			wantFixed:   []string{}, wantNew: []string{"tests::test_mul"},
		},
		{
			// tsc's clean run is silent: the empty capture is claimable only by
			// adoption from the failing run's parser + the argv hint.
			tool: "tsc", argv: []string{"tsc"}, prevSc: "fail", curSc: "pass",
			wantFailing: []string{}, wantFixed: []string{"ts/a.ts", "ts/b.ts"}, wantNew: []string{},
		},
		{
			tool: "tsc", argv: []string{"npx", "tsc"}, prevSc: "pass", curSc: "fail",
			wantFailing: []string{"ts/a.ts", "ts/b.ts"},
			wantFixed:   []string{}, wantNew: []string{"ts/a.ts", "ts/b.ts"},
		},
		{
			// Both eras: plain prev vs pretty cur — ts/b.ts is unaccounted
			// (still exit≠0, no pass evidence) ⇒ failing-only claim.
			tool: "tsc", argv: []string{"tsc"}, prevSc: "fail", curSc: "pretty-fail",
			wantFailing: []string{"ts/a.ts"}, wantNilPair: true,
		},
		{
			tool: "eslint", argv: []string{"eslint", "src/"}, prevSc: "fail", curSc: "pass",
			wantFailing: []string{},
			wantFixed:   []string{"/home/dev/fixture/src/bad.js"},
			wantNew:     []string{},
		},
	}
	for _, c := range cases {
		t.Run(c.tool+":"+c.prevSc+"→"+c.curSc, func(t *testing.T) {
			prev := loadCapture(t, c.tool, c.prevSc)
			cur := loadCapture(t, c.tool, c.curSc)
			got := Extract(c.argv, nil, &prev, cur, "")
			if got == nil {
				t.Fatal("Extract returned nil, want a claim")
			}
			if got.Tool != c.tool {
				t.Fatalf("tool=%s want %s", got.Tool, c.tool)
			}
			if !reflect.DeepEqual(got.Failing, c.wantFailing) {
				t.Errorf("failing=%v want %v", got.Failing, c.wantFailing)
			}
			if c.wantNilPair {
				if got.Fixed != nil || got.New != nil {
					t.Errorf("fixed=%v new=%v want nil pair", got.Fixed, got.New)
				}
				return
			}
			if !reflect.DeepEqual(got.Fixed, c.wantFixed) || !reflect.DeepEqual(got.New, c.wantNew) {
				t.Errorf("fixed=%v new=%v want %v/%v", got.Fixed, got.New, c.wantFixed, c.wantNew)
			}
		})
	}
}

// Runs that are not a test verdict must refuse to parse.
func TestTools_refusals(t *testing.T) {
	collecterr := loadCapture(t, "pytest", "collecterr") // exit 2: interrupted collection
	if got := Extract([]string{"pytest"}, nil, nil, collecterr, ""); got != nil {
		t.Errorf("pytest collecterr (exit 2): claim=%+v want nil", got)
	}

	// A clean-empty run without an argv hint has no output evidence at all.
	tscFail := loadCapture(t, "tsc", "fail")
	if got := Extract([]string{"npm", "run", "typecheck"}, nil, &tscFail, Run{Exit: 0}, ""); got != nil {
		t.Errorf("hint-less silent-clean: claim=%+v want nil", got)
	}

	// Forcing bridges the missing hint — same runs, --tool tsc.
	got := Extract([]string{"npm", "run", "typecheck"}, nil, &tscFail, Run{Exit: 0}, "tsc")
	if got == nil || !reflect.DeepEqual(got.Fixed, []string{"ts/a.ts", "ts/b.ts"}) {
		t.Errorf("forced tsc over silent-clean: got %+v", got)
	}
}

// Blocked flags silence each tool even on parseable output.
func TestTools_blockedFlags(t *testing.T) {
	cases := []struct {
		tool string
		argv []string
	}{
		{"pytest", []string{"pytest", "--co"}},
		{"pytest", []string{"pytest", "-p", "no:terminal"}},
		{"jest", []string{"jest", "--watch"}},
		{"jest", []string{"jest", "--reporters=default"}},
		{"vitest", []string{"vitest", "--reporter", "json"}},
		{"cargo-test", []string{"cargo", "test", "--message-format", "json"}},
		{"tsc", []string{"tsc", "--incremental"}},
		{"tsc", []string{"tsc", "-b"}},
		{"eslint", []string{"eslint", "--max-warnings", "0", "src/"}},
		{"eslint", []string{"eslint", "-f", "json", "src/"}},
	}
	for _, c := range cases {
		t.Run(c.tool+":"+c.argv[len(c.argv)-1], func(t *testing.T) {
			fail := loadCapture(t, c.tool, "fail")
			if got := Extract(c.argv, nil, nil, fail, ""); got != nil {
				t.Errorf("claim=%+v want nil (blocked flag)", got)
			}
		})
	}
}

// A warnings-only eslint run exits 0, still prints a report, and its zero-error
// output is the global clean-run proof.
func TestESLint_warningsOnly(t *testing.T) {
	prev := loadCapture(t, "eslint", "fail")
	warnOnly := Run{Exit: 0, Output: []byte(
		"\n/w/src/ok.js\n" +
			"  1:5  warning  'x' is never reassigned. Use 'const' instead  prefer-const\n" +
			"\n✖ 1 problem (0 errors, 1 warning)\n",
	)}
	got := Extract([]string{"eslint", "src/"}, nil, &prev, warnOnly, "")
	if got == nil {
		t.Fatal("nil claim")
	}
	if len(got.Failing) != 0 {
		t.Errorf("failing=%v want [] (warnings never fail)", got.Failing)
	}
	if got.Fixed == nil || len(got.Fixed) != 1 {
		t.Errorf("fixed=%v want the previously-failing file (zero-error report = clean-run proof)", got.Fixed)
	}
}

// The cargo failures: block must corroborate the FAILED verdict lines.
func TestCargo_failuresBlockCrossCheck(t *testing.T) {
	// Verdict says test_mul, block says test_other: identity disagreement.
	out := "running 2 tests\n" +
		"test tests::test_mul ... FAILED\n" +
		"test tests::test_add ... ok\n" +
		"\nfailures:\n    tests::test_other\n\n" +
		"test result: FAILED. 1 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"
	if got := Extract([]string{"cargo", "test"}, nil, nil, Run{Exit: 101, Output: []byte(out)}, ""); got != nil {
		t.Errorf("claim=%+v want nil (failures block disagrees)", got)
	}
}

// An ignored cargo test is positively not-run: it defeats the global clean-run
// proof for that identity (skipping a failure is not fixing it).
func TestCargo_ignoredIsNotRun(t *testing.T) {
	prev := loadCapture(t, "cargo-test", "fail")
	cur := "running 3 tests\n" +
		"test tests::test_add ... ok\n" +
		"test tests::test_mul ... ignored\n" +
		"test tests::test_zero ... ok\n" +
		"\ntest result: ok. 2 passed; 0 failed; 1 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"
	got := Extract([]string{"cargo", "test"}, nil, &prev, Run{Exit: 0, Output: []byte(cur)}, "")
	if got == nil {
		t.Fatal("nil claim")
	}
	if got.Fixed != nil || got.New != nil {
		t.Errorf("fixed=%v new=%v want nil pair (ignored ≠ fixed)", got.Fixed, got.New)
	}
}

// A fully-skipped pytest file is positively not-run, defeating the global
// clean-run proof for that file.
func TestPytest_fullySkippedIsNotRun(t *testing.T) {
	prev := loadCapture(t, "pytest", "fail")
	cur := "============================= test session starts ==============================\n" +
		"collected 4 items\n\n" +
		"tests/test_math.py sss                                                   [ 75%]\n" +
		"tests/test_str.py .                                                      [100%]\n\n" +
		"========================= 1 passed, 3 skipped in 0.01s ==========================\n"
	got := Extract([]string{"pytest"}, nil, &prev, Run{Exit: 0, Output: []byte(cur)}, "")
	if got == nil {
		t.Fatal("nil claim")
	}
	if got.Fixed != nil || got.New != nil {
		t.Errorf("fixed=%v new=%v want nil pair (skipped ≠ fixed)", got.Fixed, got.New)
	}
}

// Composite output carrying two tools' shapes is ambiguous ⇒ nil.
func TestExtract_compositeAmbiguity(t *testing.T) {
	tscFail := loadCapture(t, "tsc", "fail")
	esFail := loadCapture(t, "eslint", "fail")
	combined := append(append([]byte{}, tscFail.Output...), esFail.Output...)
	if got := Extract([]string{"npm", "run", "check"}, nil, nil, Run{Exit: 1, Output: combined}, ""); got != nil {
		t.Errorf("claim=%+v want nil (two fingerprints in one output)", got)
	}
}
