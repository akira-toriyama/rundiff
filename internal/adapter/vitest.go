package adapter

import (
	"regexp"
	"strings"
)

// vitest. Identity = the test FILE path, from the union of ` FAIL  file > test`
// block headers and the `❯ file (N tests | M failed)` listing lines. The
// fingerprint is the padded `Test Files` + `Tests` summary pair (no colon —
// jest's `Test Suites:`/`Tests:` never match). An `Unhandled Errors` section
// nils the run: those failures are attributable to no file.
//
//	✓ vtests/str.test.js (1 test) 1ms
//	❯ vtests/math.test.js (2 tests | 1 failed) 4ms
//	FAIL  vtests/math.test.js > multiplies
//	Test Files  1 failed | 1 passed (2)
//	     Tests  1 failed | 2 passed (3)
//
// Reconciliation: distinct failing files must equal the Test Files line's
// `N failed` (missing segment = 0).
var (
	reVtFiles    = regexp.MustCompile(`^\s*Test Files\s\s+(.+)$`)
	reVtTests    = regexp.MustCompile(`^\s*Tests\s\s+.*\(\d+\)`)
	reVtFailHdr  = regexp.MustCompile(`^\s*FAIL\s+(\S+)`)
	reVtFailList = regexp.MustCompile(`^\s*❯\s+(\S+)\s+\(.*failed`)
	reVtPassList = regexp.MustCompile(`^\s*✓\s+(\S+)\s+\(`)
	reVtNFailed  = regexp.MustCompile(`(\d+) failed`)
	reVtNPassed  = regexp.MustCompile(`(\d+) passed`)
)

type vitest struct{}

func init() { register(vitest{}) }

func (vitest) name() string { return "vitest" }

func (vitest) hint(argv []string) bool { return hasBase(argv, "vitest") }

func (vitest) match(lines []string) bool {
	files, tests := false, false
	for _, l := range lines {
		if reVtFiles.MatchString(l) {
			files = true
		}
		if reVtTests.MatchString(l) {
			tests = true
		}
	}
	return files && tests
}

func (vitest) blockedFlags(argv []string) bool {
	return hasFlag(argv, "--reporter", "--watch", "--ui")
}

func (vitest) silentWhenClean() bool { return false }

func (vitest) parse(lines []string, exit int) (parseResult, bool) {
	if exit != 0 && exit != 1 {
		return parseResult{}, false
	}
	res := parseResult{failing: map[string]struct{}{}, passing: map[string]struct{}{}}
	var filesLine string
	for _, l := range lines {
		if strings.Contains(l, "Unhandled Error") {
			return parseResult{}, false // failures with no owning file
		}
		if m := reVtFailHdr.FindStringSubmatch(l); m != nil {
			res.failing[m[1]] = struct{}{}
			continue
		}
		if m := reVtFailList.FindStringSubmatch(l); m != nil {
			res.failing[m[1]] = struct{}{}
			continue
		}
		if m := reVtPassList.FindStringSubmatch(l); m != nil {
			res.passing[m[1]] = struct{}{}
			continue
		}
		if m := reVtFiles.FindStringSubmatch(l); m != nil {
			filesLine = m[1]
		}
	}
	if filesLine == "" {
		return parseResult{}, false
	}
	if barCount(reVtNFailed, filesLine) != len(res.failing) {
		return parseResult{}, false
	}
	res.cleanRun = exit == 0 && len(res.failing) == 0 && barCount(reVtNPassed, filesLine) > 0
	return res, true
}
