package adapter

import "regexp"

// tsc. Identity = the FILE path from each diagnostic line, deduped — line/col
// shift on unrelated edits and a fix often morphs one TS code into another at
// the same site, so file-level is the honest compiler granularity. Both output
// eras are parsed:
//
//	plain  (piped — rundiff's capture mode):  ts/a.ts(4,7): error TS2322: …
//	pretty (--pretty / forced color):         ts/a.ts:4:7 - error TS2322: …
//
// Piped tsc prints NO summary line, so `Found N errors` reconciles only when
// present (pretty). tsc prints nothing on success — silentWhenClean — so a
// clean run is claimable only via adoption (the other run matched + argv
// hint / --tool agree; see Extract).
var (
	reTscPlain = regexp.MustCompile(`^(.+?)\(\d+,\d+\): error TS\d{4,5}: `)
	reTscPret  = regexp.MustCompile(`^(.+?):\d+:\d+ - error TS\d{4,5}: `)
	reTscFound = regexp.MustCompile(`^Found (\d+) errors?\b`)
)

type tsc struct{}

func init() { register(tsc{}) }

func (tsc) name() string { return "tsc" }

func (tsc) hint(argv []string) bool { return invokes(argv, "tsc", "vue-tsc") }

func (tsc) match(lines []string) bool {
	for _, l := range lines {
		if reTscPlain.MatchString(l) || reTscPret.MatchString(l) {
			return true
		}
	}
	return false
}

func (tsc) blockedFlags(argv []string) bool {
	// watch never terminates; build/incremental may re-report only a subset of
	// diagnostics across runs — uncertainty ⇒ silence.
	return hasFlag(argv, "--watch", "-w", "-b", "--build", "--incremental")
}

func (tsc) selectionFlags([]string) bool { return false } // tsc has no name-level selection

func (tsc) silentWhenClean() bool { return true }

func (tsc) parse(lines []string, exit int) (parseResult, bool) {
	// 1 = diagnostics, outputs generated; 2 = diagnostics, outputs skipped.
	if exit != 1 && exit != 2 {
		return parseResult{}, false // clean runs are silent — nothing to parse
	}
	res := parseResult{failing: map[string]struct{}{}, passing: map[string]struct{}{}}
	errLines := 0
	found := -1
	for _, l := range lines {
		if m := reTscPlain.FindStringSubmatch(l); m != nil {
			res.failing[m[1]] = struct{}{}
			errLines++
			continue
		}
		if m := reTscPret.FindStringSubmatch(l); m != nil {
			res.failing[m[1]] = struct{}{}
			errLines++
			continue
		}
		if m := reTscFound.FindStringSubmatch(l); m != nil {
			found = atoiDigits(m[1])
		}
	}
	if errLines == 0 {
		return parseResult{}, false
	}
	if found >= 0 && found != errLines {
		return parseResult{}, false // summary disagrees with the lines: torn
	}
	return res, true
}
