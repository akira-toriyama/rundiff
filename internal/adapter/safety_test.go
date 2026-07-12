package adapter

import (
	"reflect"
	"strings"
	"testing"
)

// Regression tests for the adversarial-review findings: every case here was a
// confirmed FALSE-CLAIM path (or its guard) before the fix. Each asserts the
// pair (or the whole claim) is withheld — never that a lie is emitted.

// go: `ok pkg 0.4s [no tests to run]` positively reports zero tests executed —
// never pass evidence, so a previously-failing package stays unaccounted.
func TestGoTest_noTestsToRunIsNotPassEvidence(t *testing.T) {
	prev := Run{Exit: 1, Output: []byte("FAIL\texample.com/m\t0.4s\nFAIL\n")}
	cur := Run{Exit: 0, Output: []byte("ok  \texample.com/m\t0.39s [no tests to run]\n")}
	got := Extract(goArgv, nil, &prev, cur, "")
	if got == nil {
		t.Fatal("nil claim, want failing-only")
	}
	if got.Fixed != nil || got.New != nil {
		t.Errorf("fixed=%v new=%v want nil pair (no tests ran)", got.Fixed, got.New)
	}
}

// go: a -v run showing --- SKIP marks drops ALL package pass evidence — the
// trailer does not say which package the skipped (possibly previously-failing)
// test lives in.
func TestGoTest_skipMarksDropPassEvidence(t *testing.T) {
	prev := Run{Exit: 1, Output: []byte("FAIL\texample.com/m\t0.4s\nFAIL\n")}
	cur := Run{Exit: 0, Output: []byte(
		"=== RUN   TestBoom\n--- SKIP: TestBoom (0.00s)\nPASS\nok  \texample.com/m\t0.2s\n")}
	got := Extract([]string{"go", "test", "-v", "./..."}, nil, &prev, cur, "")
	if got == nil {
		t.Fatal("nil claim")
	}
	if got.Fixed != nil {
		t.Errorf("fixed=%v want nil pair (a skip mark hides which package stopped running a test)", got.Fixed)
	}
}

// Name-level selection flags withhold the pair: a rename silently deselects a
// still-failing test between runs with identical argv, so a green run proves
// nothing. Same flag arriving via the environment (GOFLAGS, PYTEST_ADDOPTS)
// must gate identically.
func TestSelectionFlagsWithholdThePair(t *testing.T) {
	goFail := loadCapture(t, "go-test", "fail")
	goPass := loadCapture(t, "go-test", "pass")
	pyFail := loadCapture(t, "pytest", "fail")
	pyPass := loadCapture(t, "pytest", "pass")
	jestFail := loadCapture(t, "jest", "fail")
	jestPass := loadCapture(t, "jest", "pass")
	vtFail := loadCapture(t, "vitest", "fail")
	vtPass := loadCapture(t, "vitest", "pass")
	cgFail := loadCapture(t, "cargo-test", "fail")
	cgPass := loadCapture(t, "cargo-test", "pass")

	cases := []struct {
		name string
		argv []string
		env  []string
		prev Run
		cur  Run
	}{
		{"go -run", []string{"go", "test", "-run", "TestX", "./..."}, nil, goFail, goPass},
		{"go --skip", []string{"go", "test", "--skip", "TestX", "./..."}, nil, goFail, goPass},
		{"go GOFLAGS env", []string{"go", "test", "./..."}, []string{"-run=TestX"}, goFail, goPass},
		{"pytest -k", []string{"pytest", "-k", "mul"}, nil, pyFail, pyPass},
		{"pytest --lf", []string{"pytest", "--lf"}, nil, pyFail, pyPass},
		{"pytest ADDOPTS env", []string{"pytest"}, []string{"-k", "mul"}, pyFail, pyPass},
		{"jest -t", []string{"jest", "-t", "multiplies"}, nil, jestFail, jestPass},
		{"jest --onlyChanged", []string{"jest", "--onlyChanged"}, nil, jestFail, jestPass},
		{"jest --shard", []string{"jest", "--shard=1/2"}, nil, jestFail, jestPass},
		{"vitest --changed", []string{"vitest", "run", "--changed"}, nil, vtFail, vtPass},
		{"cargo name filter", []string{"cargo", "test", "test_mul"}, nil, cgFail, cgPass},
		{"cargo -- filter", []string{"cargo", "test", "--", "test_mul"}, nil, cgFail, cgPass},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Extract(c.argv, c.env, &c.prev, c.cur, "")
			if got == nil {
				return // whole-claim abstention is also safe
			}
			if got.Fixed != nil || got.New != nil {
				t.Errorf("fixed=%v new=%v want nil pair under selection", got.Fixed, got.New)
			}
		})
	}
}

// pytest: a partial skip (`.s` progress) means the previously-failing test may
// be the skipped one — no pass evidence for the file.
func TestPytest_partialSkipIsNotPassEvidence(t *testing.T) {
	prev := Run{Exit: 1, Output: []byte(
		"============================= test session starts ==============================\n" +
			"tests/test_x.py .F                                                       [100%]\n" +
			"FAILED tests/test_x.py::test_boom - assert False\n" +
			"========================= 1 failed, 1 passed in 0.01s ==========================\n")}
	cur := Run{Exit: 0, Output: []byte(
		"============================= test session starts ==============================\n" +
			"tests/test_x.py .s                                                       [100%]\n" +
			"========================= 1 passed, 1 skipped in 0.01s ==========================\n")}
	got := Extract([]string{"pytest"}, nil, &prev, cur, "")
	if got == nil {
		t.Fatal("nil claim")
	}
	if got.Fixed != nil {
		t.Errorf("fixed=%v want nil pair (the skip may be the old failure)", got.Fixed)
	}
}

// pytest: `N deselected` in the final bar (a -k effect, possibly env-injected
// and invisible in argv) drops all pass evidence.
func TestPytest_deselectedBarDropsPassEvidence(t *testing.T) {
	prev := Run{Exit: 1, Output: []byte(
		"============================= test session starts ==============================\n" +
			"tests/test_x.py .F                                                       [100%]\n" +
			"FAILED tests/test_x.py::test_boom - assert False\n" +
			"========================= 1 failed, 1 passed in 0.01s ==========================\n")}
	cur := Run{Exit: 0, Output: []byte(
		"============================= test session starts ==============================\n" +
			"tests/test_x.py .                                                        [100%]\n" +
			"==================== 1 passed, 1 deselected in 0.01s ===========================\n")}
	got := Extract([]string{"pytest"}, nil, &prev, cur, "")
	if got == nil {
		t.Fatal("nil claim")
	}
	if got.Fixed != nil {
		t.Errorf("fixed=%v want nil pair (deselection hides the old failure)", got.Fixed)
	}
}

// jest: a skipped suite (describe.skip) prints no per-suite line and a skipped
// test (it.skip) hides inside a PASS suite — either skip count kills all pass
// evidence.
func TestJest_skippedDropsPassEvidence(t *testing.T) {
	prev := loadCapture(t, "jest", "fail")
	cases := []struct {
		name string
		cur  string
	}{
		{"describe.skip: suite vanishes", "PASS __tests__/str.test.js\n" +
			"Test Suites: 1 skipped, 1 passed, 1 of 2 total\n" +
			"Tests:       1 skipped, 1 passed, 2 total\n" +
			"Ran all test suites.\n"},
		{"it.skip inside a PASS suite", "PASS __tests__/str.test.js\nPASS __tests__/math.test.js\n" +
			"Test Suites: 2 passed, 2 total\n" +
			"Tests:       1 skipped, 2 passed, 3 total\n" +
			"Ran all test suites.\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Extract([]string{"jest"}, nil, &prev, Run{Exit: 0, Output: []byte(c.cur)}, "")
			if got == nil {
				return
			}
			if got.Fixed != nil {
				t.Errorf("fixed=%v want nil pair (skipped tests hide the old failure)", got.Fixed)
			}
		})
	}
}

// vitest: a fully-skipped file (↓) and a ✓ file with a skipped segment are
// positively not-(fully-)run.
func TestVitest_skipMarkersAreNotPassEvidence(t *testing.T) {
	prev := loadCapture(t, "vitest", "fail")
	cases := []struct {
		name string
		cur  string
	}{
		{"fully-skipped file", " ↓ vtests/math.test.js (2 tests | 2 skipped)\n ✓ vtests/str.test.js (1 test) 1ms\n\n" +
			" Test Files  1 passed | 1 skipped (2)\n      Tests  1 passed | 2 skipped (3)\n"},
		{"skipped segment in a green file", " ✓ vtests/math.test.js (2 tests | 1 skipped) 2ms\n ✓ vtests/str.test.js (1 test) 1ms\n\n" +
			" Test Files  2 passed (2)\n      Tests  2 passed | 1 skipped (3)\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Extract([]string{"vitest", "run"}, nil, &prev, Run{Exit: 0, Output: []byte(c.cur)}, "")
			if got == nil {
				return
			}
			if got.Fixed != nil {
				t.Errorf("fixed=%v want nil pair (vitest reported skips)", got.Fixed)
			}
		})
	}
}

// vitest workspace mode: the |project| tag is part of the identity, so files
// running under different projects never collapse into one identity.
func TestVitest_projectTagInIdentity(t *testing.T) {
	out := " FAIL  |alpha| vtests/math.test.js > multiplies\n" +
		" ❯ |alpha| vtests/math.test.js (2 tests | 1 failed) 4ms\n" +
		" ✓ |beta| vtests/math.test.js (2 tests) 2ms\n\n" +
		" Test Files  1 failed | 1 passed (2)\n      Tests  1 failed | 3 passed (4)\n"
	got := Extract([]string{"vitest", "run"}, nil, nil, Run{Exit: 1, Output: []byte(out)}, "")
	if got == nil {
		t.Fatal("nil claim")
	}
	want := []string{"|alpha| vtests/math.test.js"}
	if !reflect.DeepEqual(got.Failing, want) {
		t.Errorf("failing=%v want %v", got.Failing, want)
	}
}

// cargo: rust ≥1.61 prints `ignored, <reason>` — still a not-run marker.
func TestCargo_ignoredWithReasonIsNotRun(t *testing.T) {
	prev := loadCapture(t, "cargo-test", "fail")
	cur := "running 3 tests\n" +
		"test tests::test_add ... ok\n" +
		"test tests::test_mul ... ignored, flaky\n" +
		"test tests::test_zero ... ok\n" +
		"\ntest result: ok. 2 passed; 0 failed; 1 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"
	got := Extract([]string{"cargo", "test"}, nil, &prev, Run{Exit: 0, Output: []byte(cur)}, "")
	if got == nil {
		t.Fatal("nil claim")
	}
	if got.Fixed != nil {
		t.Errorf("fixed=%v want nil pair (ignored ≠ fixed)", got.Fixed)
	}
}

// cargo: the same unqualified test name in two binaries (workspace crates)
// would merge two tests into one identity — refuse the run.
func TestCargo_duplicateNameAcrossBinariesRefused(t *testing.T) {
	out := "running 1 test\ntest tests::test_util ... FAILED\n" +
		"\nfailures:\n    tests::test_util\n\n" +
		"test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n" +
		"\nrunning 1 test\ntest tests::test_util ... ok\n" +
		"\ntest result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"
	if got := Extract([]string{"cargo", "test"}, nil, nil, Run{Exit: 101, Output: []byte(out)}, ""); got != nil {
		t.Errorf("claim=%+v want nil (colliding identities across binaries)", got)
	}
}

// A tool name in a non-command argv token (an npm script named "tsc") must not
// narrow the candidate set: composite output stays ambiguous ⇒ nil.
func TestHint_scriptNameCollisionDoesNotNarrow(t *testing.T) {
	tscFail := loadCapture(t, "tsc", "fail")
	esFail := loadCapture(t, "eslint", "fail")
	combined := append(append([]byte{}, tscFail.Output...), esFail.Output...)
	if got := Extract([]string{"npm", "run", "tsc"}, nil, nil, Run{Exit: 1, Output: combined}, ""); got != nil {
		t.Errorf("claim=%+v want nil (script-name hint must not defeat the composite refusal)", got)
	}
	// And the collision must not supply silent-clean adoption evidence either.
	if got := Extract([]string{"npm", "run", "tsc"}, nil, &tscFail, Run{Exit: 0}, ""); got != nil {
		t.Errorf("claim=%+v want nil (script-name hint must not enable adoption)", got)
	}
}

// A6: a cross-tool pair (the wrapped script switched runners) yields a
// failing-only claim from the current run.
func TestExtract_crossTool(t *testing.T) {
	prev := loadCapture(t, "jest", "fail")
	cur := loadCapture(t, "vitest", "fail")
	got := Extract([]string{"npm", "test"}, nil, &prev, cur, "")
	if got == nil {
		t.Fatal("nil claim, want failing-only from the vitest run")
	}
	if got.Tool != "vitest" || len(got.Failing) == 0 {
		t.Errorf("tool=%s failing=%v want vitest failing set", got.Tool, got.Failing)
	}
	if got.Fixed != nil || got.New != nil {
		t.Errorf("fixed=%v new=%v want nil pair (identities from different tools are incomparable)", got.Fixed, got.New)
	}
}

// Per-identity pass evidence is load-bearing (not the global proof): removing
// the PASS line for the previously-failing jest suite — everything else green —
// must withhold the pair.
func TestJest_passEvidenceIsPerIdentity(t *testing.T) {
	prev := loadCapture(t, "jest", "fail")
	pass := loadCapture(t, "jest", "pass")
	stripped := strings.Replace(string(pass.Output), "PASS __tests__/math.test.js\n", "", 1)
	// Keep the summary consistent with the visible lines: one suite shown.
	stripped = strings.Replace(stripped, "Test Suites: 2 passed, 2 total", "Test Suites: 1 passed, 1 total", 1)
	stripped = strings.Replace(stripped, "Tests:       3 passed, 3 total", "Tests:       1 passed, 1 total", 1)
	got := Extract([]string{"jest"}, nil, &prev, Run{Exit: 0, Output: []byte(stripped)}, "")
	if got == nil {
		return // abstention is safe
	}
	if got.Fixed != nil {
		t.Errorf("fixed=%v want nil pair (no per-suite pass line for the old failure)", got.Fixed)
	}
}

// Go's flag package accepts one or two dashes: --json must block like -json.
func TestGoTest_doubleDashBlockedFlags(t *testing.T) {
	fail := loadCapture(t, "go-test", "fail")
	pass := loadCapture(t, "go-test", "pass")
	if got := Extract([]string{"go", "test", "--json", "./..."}, nil, &fail, pass, ""); got != nil {
		t.Errorf("claim=%+v want nil (--json is -json)", got)
	}
}
