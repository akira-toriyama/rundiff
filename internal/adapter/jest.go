package adapter

import (
	"regexp"
	"strings"
)

// jest. Identity = the suite line payload from `FAIL path` / `PASS path`
// (trailing ` (N.NNN s)` duration stripped; a --projects tag stays inside — it
// is stable and part of the honest identity). The fingerprint pairs the
// `Test Suites:` summary (unique — vitest says `Test Files`) with the final
// `Ran all test suites` line, so a truncated run never parses.
//
//	PASS __tests__/str.test.js
//	FAIL __tests__/math.test.js (0.35 s)
//	Test Suites: 1 failed, 1 passed, 2 total
//	Ran all test suites.
//
// Reconciliation: distinct FAIL identities must equal the summary's
// `N failed` (a missing "failed" segment means 0).
var (
	reJestFail     = regexp.MustCompile(`^FAIL (.+)$`)
	reJestPass     = regexp.MustCompile(`^PASS (.+)$`)
	reJestSuites   = regexp.MustCompile(`^Test Suites:\s+(.+)$`)
	reJestTests    = regexp.MustCompile(`^Tests:\s+(.+)$`)
	reJestRan      = regexp.MustCompile(`^Ran all test suites`)
	reJestDuration = regexp.MustCompile(`\s+\([0-9.]+\s*s\)$`)
	reJestNFailed  = regexp.MustCompile(`(\d+) failed`)
	reJestNPassed  = regexp.MustCompile(`(\d+) passed`)
	reJestNSkipped = regexp.MustCompile(`(\d+) (?:skipped|todo)`)
)

type jest struct{}

func init() { register(jest{}) }

func (jest) name() string { return "jest" }

func (jest) hint(argv []string) bool { return invokes(argv, "jest") }

func (jest) match(lines []string) bool {
	suites, ran := false, false
	for _, l := range lines {
		if reJestSuites.MatchString(l) {
			suites = true
		}
		if reJestRan.MatchString(l) {
			ran = true
		}
	}
	return suites && ran
}

func (jest) blockedFlags(argv []string) bool {
	return hasFlag(argv, "--json", "--reporters", "--listTests", "--watch", "--watchAll", "--outputFile")
}

func (jest) selectionFlags(argv []string) bool {
	// Name- and git-state-level selection: the selected set varies between
	// runs with identical argv (a rename escapes -t; --onlyChanged/--shard
	// follow the working tree), so a green subset run proves nothing about a
	// deselected suite. Positional args are path REGEXES — path-level — and
	// stay allowed.
	return hasFlag(argv, "-t", "--testNamePattern", "-o", "--onlyChanged",
		"--changedSince", "--changedFilesWithAncestor", "--lastCommit",
		"--shard", "--onlyFailures", "-f")
}

func (jest) silentWhenClean() bool { return false }

// Identity = the file; a fully-filtered file is omitted from the report, so it
// carries no pass evidence (A7 withholds).
func (jest) pairNeedsHint() bool { return false }

func (jest) parse(lines []string, exit int) (parseResult, bool) {
	if exit != 0 && exit != 1 {
		return parseResult{}, false
	}
	res := parseResult{failing: map[string]struct{}{}, passing: map[string]struct{}{}}
	var suites, tests string
	for _, l := range lines {
		if m := reJestFail.FindStringSubmatch(l); m != nil {
			res.failing[suitePayload(m[1])] = struct{}{}
			continue
		}
		if m := reJestPass.FindStringSubmatch(l); m != nil {
			res.passing[suitePayload(m[1])] = struct{}{}
			continue
		}
		if m := reJestSuites.FindStringSubmatch(l); m != nil {
			suites = m[1]
			continue
		}
		if m := reJestTests.FindStringSubmatch(l); m != nil {
			tests = m[1]
		}
	}
	if suites == "" || tests == "" {
		return parseResult{}, false
	}
	if barCount(reJestNFailed, suites) != len(res.failing) {
		return parseResult{}, false // a FAIL line was lost (or invented): torn output
	}
	// Skipped/todo tests or suites (it.skip, describe.skip, xdescribe): jest
	// prints PASS for a suite whose skipped test might be the previously
	// failing one, and prints nothing at all for a fully-skipped suite. Either
	// way, per-suite pass evidence can no longer be trusted — drop it all.
	if barCount(reJestNSkipped, suites) > 0 || barCount(reJestNSkipped, tests) > 0 {
		res.passing = map[string]struct{}{}
	}
	res.cleanRun = exit == 0 && len(res.failing) == 0 && barCount(reJestNPassed, suites) > 0
	return res, true
}

// suitePayload strips the trailing duration jest sometimes appends to a
// PASS/FAIL suite line (run-varying chrome, not identity).
func suitePayload(s string) string {
	return strings.TrimSpace(reJestDuration.ReplaceAllString(s, ""))
}
