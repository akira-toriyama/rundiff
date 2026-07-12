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
	reJestRan      = regexp.MustCompile(`^Ran all test suites`)
	reJestDuration = regexp.MustCompile(`\s+\([0-9.]+\s*s\)$`)
	reJestNFailed  = regexp.MustCompile(`(\d+) failed`)
	reJestNPassed  = regexp.MustCompile(`(\d+) passed`)
)

type jest struct{}

func init() { register(jest{}) }

func (jest) name() string { return "jest" }

func (jest) hint(argv []string) bool { return hasBase(argv, "jest") }

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

func (jest) silentWhenClean() bool { return false }

func (jest) parse(lines []string, exit int) (parseResult, bool) {
	if exit != 0 && exit != 1 {
		return parseResult{}, false
	}
	res := parseResult{failing: map[string]struct{}{}, passing: map[string]struct{}{}}
	var summary string
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
			summary = m[1]
		}
	}
	if summary == "" {
		return parseResult{}, false
	}
	if barCount(reJestNFailed, summary) != len(res.failing) {
		return parseResult{}, false // a FAIL line was lost (or invented): torn output
	}
	res.cleanRun = exit == 0 && len(res.failing) == 0 && barCount(reJestNPassed, summary) > 0
	return res, true
}

// suitePayload strips the trailing duration jest sometimes appends to a
// PASS/FAIL suite line (run-varying chrome, not identity).
func suitePayload(s string) string {
	return strings.TrimSpace(reJestDuration.ReplaceAllString(s, ""))
}
