package adapter

import (
	"regexp"
	"strings"
)

// go test. Identity = the PACKAGE import path from the per-package trailer
// lines — the file notion go prints machine-stably. `--- FAIL: TestX` names
// collide across packages and interleave under parallelism, and test-level
// pass evidence needs -v, so package-level is what can be vouched for.
//
//	ok  	example.com/mod/pkg	0.49s        pass evidence (also "(cached)")
//	FAIL	example.com/mod/pkg	0.49s        failing
//	FAIL	example.com/mod/pkg [build failed] failing (a build error IS that
//	                                         package failing — not a fixed/new)
//	?   	example.com/mod/pkg	[no test files]  NOT evidence either way
//
// go has no global count summary, so reconciliation is structural: a failing
// exit needs at least one FAIL trailer, a clean exit needs none, and a
// `--- FAIL:` block with no FAIL trailer at all means the trailer was lost
// (torn/truncated output) ⇒ no claim.
var (
	reGoOK     = regexp.MustCompile(`^ok\s+(\S+)\s+(\(cached\)|[0-9.]+s)`)
	reGoFAIL   = regexp.MustCompile(`^FAIL\s+(\S+)\s+(\[|\(cached\)|[0-9.]+s)`)
	reGoNoTest = regexp.MustCompile(`^\?\s+(\S+)\s+\[no test files\]`)
	reGoRun    = regexp.MustCompile(`^=== RUN\s+\S+`)
	reGoMark   = regexp.MustCompile(`^--- (FAIL|PASS): `)
)

type goTest struct{}

func init() { register(goTest{}) }

func (goTest) name() string { return "go-test" }

func (goTest) hint(argv []string) bool {
	return invokes(argv, "go") && hasWord(argv, "test")
}

func (goTest) match(lines []string) bool {
	for _, l := range lines {
		if reGoOK.MatchString(l) || reGoFAIL.MatchString(l) || reGoRun.MatchString(l) || reGoMark.MatchString(l) {
			return true
		}
	}
	return false
}

func (goTest) blockedFlags(argv []string) bool {
	// -json is a different format (and better data for whoever asked for it);
	// -fuzz runs indefinitely and reports nothing trailer-shaped. Go's flag
	// package accepts one or two dashes — list both spellings.
	return hasFlag(argv, "-json", "--json", "-fuzz", "--fuzz", "-bench", "--bench")
}

func (goTest) selectionFlags(argv []string) bool {
	// -run/-skip select by test NAME: a rename silently deselects a
	// still-failing test under identical argv. `go test` also accepts every
	// test flag under a `-test.` prefix (go help testflag) and with one or two
	// dashes, so list all spellings.
	return hasFlag(argv, "-run", "--run", "-skip", "--skip",
		"-test.run", "--test.run", "-test.skip", "--test.skip")
}

func (goTest) silentWhenClean() bool { return false }

func (goTest) parse(lines []string, exit int) (parseResult, bool) {
	if exit != 0 && exit != 1 {
		return parseResult{}, false // 2 = flag misuse / go's own error
	}
	res := parseResult{failing: map[string]struct{}{}, passing: map[string]struct{}{}, notRun: map[string]struct{}{}}
	failMarks, skipMarks := 0, 0
	for _, l := range lines {
		if m := reGoFAIL.FindStringSubmatch(l); m != nil {
			res.failing[m[1]] = struct{}{}
			continue
		}
		if m := reGoOK.FindStringSubmatch(l); m != nil {
			// `ok pkg 0.4s [no tests to run]` positively states ZERO tests
			// executed (a -run/-skip pattern — possibly env-injected — matched
			// nothing): the strongest not-run signal, never pass evidence.
			if strings.Contains(l, "[no tests to run]") {
				res.notRun[m[1]] = struct{}{}
			} else {
				res.passing[m[1]] = struct{}{}
			}
			continue
		}
		if m := reGoNoTest.FindStringSubmatch(l); m != nil {
			res.notRun[m[1]] = struct{}{}
			continue
		}
		// Subtest marks are indented (`    --- SKIP:`), so match after trimming
		// leading whitespace — otherwise a skipped SUBTEST slips past the wipe
		// and its parent's top-level `--- PASS`/`ok` reads as pass evidence.
		trimmed := strings.TrimLeft(l, " \t")
		if strings.HasPrefix(trimmed, "--- FAIL: ") {
			failMarks++
		}
		if strings.HasPrefix(trimmed, "--- SKIP: ") {
			skipMarks++
		}
	}
	// A -v run showing skips: some test in SOME package did not run, and go's
	// trailer does not say which. Package-level pass evidence can no longer be
	// trusted to cover a previously-failing test — drop it all (conservative;
	// non-verbose runs print no skip marker at all, a documented residual of
	// package-granularity identities).
	if skipMarks > 0 {
		res.passing = map[string]struct{}{}
	}
	// Structural reconciliation (go prints no count summary).
	if exit != 0 && len(res.failing) == 0 {
		return parseResult{}, false // the failure was something we didn't identify
	}
	if exit == 0 && len(res.failing) > 0 {
		return parseResult{}, false
	}
	if failMarks > 0 && len(res.failing) == 0 {
		return parseResult{}, false // FAIL marks with no trailer: torn output
	}
	res.cleanRun = exit == 0 && len(res.failing) == 0 && len(res.passing) > 0
	return res, true
}
