package adapter

import (
	"regexp"
	"strings"
)

// pytest. Identity = the FILE path, coarsened from failing nodeids
// (`FAILED tests/test_x.py::test_y` → `tests/test_x.py`); a collection ERROR is
// that file failing (an unimportable test file did not pass). Both anchors live
// in the short-test-summary section, printed once by the parent process — the
// least tearable lines in the output.
//
//	============================= test session starts ==============================   header
//	tests/test_math.py .F.                                                   [ 75%]   progress
//	FAILED tests/test_math.py::test_mul - assert (2 * 3) == 7                          summary
//	ERROR tests/test_broken.py                                                         summary
//	========================= 1 failed, 3 passed in 0.01s ==========================   final bar
//
// Reconciliation: the FAILED/ERROR line counts must equal the final bar's
// `N failed` / `N error(s)` counts. Pass evidence per file is its progress line
// when it contains at least one '.' and nothing beyond [.sxX] (a fully-skipped
// file is positively NOT run — skipping a failure is not fixing it). Progress
// continuation lines (a long file wraps to bare dots) carry no file path and
// are safely ignored: failing membership comes from the summary, never from
// progress chars.
var (
	rePyHeader   = regexp.MustCompile(`^=+ test session starts =+$`)
	rePyFinal    = regexp.MustCompile(`^=+ (.+) in [0-9.]+s(?: \([^)]*\))? =+$`)
	rePyFailed   = regexp.MustCompile(`^FAILED (\S+)`)
	rePyError    = regexp.MustCompile(`^ERROR (\S+)`)
	rePyPassedID = regexp.MustCompile(`^PASSED (\S+)`) // -rA / -rp summaries
	rePyProgress = regexp.MustCompile(`^(\S+\.py)\s+([.sxXEF]+)\s*(?:\[\s*\d+%\])?$`)
	rePyNFailed  = regexp.MustCompile(`(\d+) failed`)
	rePyNError   = regexp.MustCompile(`(\d+) errors?`)
	rePyNPassed  = regexp.MustCompile(`(\d+) passed`)
)

type pytest struct{}

func init() { register(pytest{}) }

func (pytest) name() string { return "pytest" }

func (pytest) hint(argv []string) bool {
	if hasBase(argv, "pytest", "py.test") {
		return true
	}
	return hasBase(argv, "python", "python3") && hasWord(argv, "pytest")
}

func (pytest) match(lines []string) bool {
	header, final := false, false
	for _, l := range lines {
		if rePyHeader.MatchString(l) {
			header = true
		}
		if rePyFinal.MatchString(l) {
			final = true
		}
	}
	return header && final
}

func (pytest) blockedFlags(argv []string) bool {
	if hasFlag(argv, "--collect-only", "--co", "--no-summary") {
		return true
	}
	// -p no:terminal (split or glued) removes the whole report.
	for i, a := range argv {
		if a == "-pno:terminal" || (a == "-p" && i+1 < len(argv) && argv[i+1] == "no:terminal") {
			return true
		}
	}
	return false
}

func (pytest) silentWhenClean() bool { return false }

func (pytest) parse(lines []string, exit int) (parseResult, bool) {
	// 2 interrupted / 3 internal / 4 usage: not a test verdict. 5 (no tests
	// collected) must not look like a green suite.
	if exit != 0 && exit != 1 {
		return parseResult{}, false
	}
	res := parseResult{failing: map[string]struct{}{}, passing: map[string]struct{}{}, notRun: map[string]struct{}{}}
	failedLines, errorLines := 0, 0
	var finalBar string
	for _, l := range lines {
		if m := rePyFailed.FindStringSubmatch(l); m != nil {
			res.failing[nodeFile(m[1])] = struct{}{}
			failedLines++
			continue
		}
		if m := rePyError.FindStringSubmatch(l); m != nil {
			res.failing[nodeFile(m[1])] = struct{}{}
			errorLines++
			continue
		}
		if m := rePyPassedID.FindStringSubmatch(l); m != nil {
			res.passing[nodeFile(m[1])] = struct{}{}
			continue
		}
		if m := rePyProgress.FindStringSubmatch(l); m != nil {
			file, chars := m[1], m[2]
			switch {
			case strings.ContainsAny(chars, "EF"):
				// Failing membership comes from the summary; the progress
				// chars only ever ADD failure evidence, never pass evidence.
			case strings.Contains(chars, "."):
				res.passing[file] = struct{}{}
			default:
				res.notRun[file] = struct{}{} // all skipped/xfailed: not run
			}
			continue
		}
		if m := rePyFinal.FindStringSubmatch(l); m != nil {
			finalBar = m[1] // last final-shaped bar wins (there is only one)
		}
	}
	if finalBar == "" {
		return parseResult{}, false // sentinel severed
	}
	if barCount(rePyNFailed, finalBar) != failedLines || barCount(rePyNError, finalBar) != errorLines {
		return parseResult{}, false // torn/truncated summary
	}
	res.cleanRun = exit == 0 && failedLines == 0 && errorLines == 0 && barCount(rePyNPassed, finalBar) > 0
	return res, true
}

// nodeFile coarsens a pytest nodeid to its file part.
func nodeFile(node string) string {
	if i := strings.Index(node, "::"); i >= 0 {
		return node[:i]
	}
	return node
}

func barCount(re *regexp.Regexp, bar string) int {
	m := re.FindStringSubmatch(bar)
	if m == nil {
		return 0
	}
	return atoiDigits(m[1])
}

// atoiDigits converts an all-digit capture (guaranteed by the regexes).
func atoiDigits(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
