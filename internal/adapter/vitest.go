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
// Workspace/projects mode prefixes each file with a plain `|project|` tag —
// the tag is part of the honest identity (the same file can run under two
// projects), so the captures include it when present.
var (
	reVtFiles    = regexp.MustCompile(`^\s*Test Files\s\s+(.+)$`)
	reVtTests    = regexp.MustCompile(`^\s*Tests\s\s+.*\(\d+\)`)
	reVtFailHdr  = regexp.MustCompile(`^\s*FAIL\s+((?:\|[^|]+\|\s+)?\S+)`)
	reVtFailList = regexp.MustCompile(`^\s*❯\s+((?:\|[^|]+\|\s+)?\S+)\s+\(.*failed`)
	// A file ✓ line always carries a `(N test[s] …)` count; a per-test ✓ line
	// (`✓ adds 1ms`, `✓ name (edge)`) never does. Requiring the test-count
	// paren is what separates a real file-pass from a test name that merely
	// looks path-shaped.
	reVtPassList = regexp.MustCompile(`^\s*✓\s+((?:\|[^|]+\|\s+)?\S+)\s+\((\d+ tests?[^)]*)\)`)
	reVtSkipList = regexp.MustCompile(`^\s*↓\s+((?:\|[^|]+\|\s+)?\S+)`)
	reVtNFailed  = regexp.MustCompile(`(\d+) failed`)
	reVtNPassed  = regexp.MustCompile(`(\d+) passed`)
	// A vitest file identity is a path: it carries a separator or a source
	// extension. A per-test name (`✓ parses`, even `✓ handles (edge) case`)
	// does not — the guard keeps such lines from minting file pass evidence.
	reVtFileish = regexp.MustCompile(`/|\.[cm]?[jt]sx?$`)
)

func looksLikeVitestFile(id string) bool {
	// Strip a leading |project| tag before the path test.
	if i := strings.LastIndex(id, "| "); i >= 0 {
		id = id[i+2:]
	}
	return reVtFileish.MatchString(id)
}

type vitest struct{}

func init() { register(vitest{}) }

func (vitest) name() string { return "vitest" }

func (vitest) hint(argv []string) bool { return invokes(argv, "vitest") }

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

func (vitest) selectionFlags(argv []string) bool {
	// Name- and git-state-level selection (see jest): a green subset run
	// proves nothing about a deselected file.
	return hasFlag(argv, "-t", "--testNamePattern", "--changed", "--shard")
}

func (vitest) silentWhenClean() bool { return false }

// Identity = the file; a fully-filtered file prints the ↓ skip marker, which is
// notRun, not pass evidence (A7 withholds).
func (vitest) pairNeedsHint() bool { return false }

func (vitest) parse(lines []string, exit int) (parseResult, bool) {
	if exit != 0 && exit != 1 {
		return parseResult{}, false
	}
	res := parseResult{failing: map[string]struct{}{}, passing: map[string]struct{}{}, notRun: map[string]struct{}{}}
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
		if m := reVtPassList.FindStringSubmatch(l); m != nil && looksLikeVitestFile(m[1]) {
			// `✓ file (2 tests | 1 skipped)` means some test in the file did
			// not run — possibly the previously-failing one, freshly skipped.
			// Pass evidence requires a skip-free paren. The file-shape guard
			// keeps an indented per-TEST `✓ name (…)` line (a test.each case)
			// from minting file-level pass evidence.
			if strings.Contains(m[2], "skipped") || strings.Contains(m[2], "todo") {
				res.notRun[m[1]] = struct{}{}
			} else {
				res.passing[m[1]] = struct{}{}
			}
			continue
		}
		if m := reVtSkipList.FindStringSubmatch(l); m != nil && looksLikeVitestFile(m[1]) {
			res.notRun[m[1]] = struct{}{} // ↓ = fully-skipped file
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
