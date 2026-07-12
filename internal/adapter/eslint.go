package adapter

import (
	"regexp"
	"strings"
)

// eslint (stylish, the default formatter). Identity = the FILE path heading a
// block that owns at least one error-severity row — warnings do not fail the
// run, so counting them would fake failures (they still show in the line
// delta). The `✖ N problems (E errors, W warnings)` line is the sentinel; the
// error-row count must reconcile with E.
//
//	/abs/path/src/bad.js
//	  1:5   error    'unused' is assigned a value but never used  no-unused-vars
//	  2:5   warning  'never' is never reassigned                  prefer-const
//	✖ 4 problems (2 errors, 2 warnings)
//
// eslint prints nothing on success — silentWhenClean — so a clean run is
// claimable only via adoption (see Extract). A warnings-only run does print
// (exit 0): its zero-error report is the global clean-run proof.
var (
	reEsRow     = regexp.MustCompile(`^\s+\d+:\d+\s+(error|warning)\s+`)
	reEsProblem = regexp.MustCompile(`^✖ \d+ problems? \((\d+) errors?, \d+ warnings?\)`)
)

type eslint struct{}

func init() { register(eslint{}) }

func (eslint) name() string { return "eslint" }

func (eslint) hint(argv []string) bool { return hasBase(argv, "eslint") }

func (eslint) match(lines []string) bool {
	row, problem := false, false
	for _, l := range lines {
		if reEsRow.MatchString(l) {
			row = true
		}
		if reEsProblem.MatchString(l) {
			problem = true
		}
	}
	return row && problem
}

func (eslint) blockedFlags(argv []string) bool {
	// A non-stylish formatter is a different language; --max-warnings makes
	// warnings drive exit 1, breaking the exit ⇔ failing-set equivalence.
	return hasFlag(argv, "-f", "--format", "--max-warnings")
}

func (eslint) silentWhenClean() bool { return true }

func (eslint) parse(lines []string, exit int) (parseResult, bool) {
	if exit != 0 && exit != 1 {
		return parseResult{}, false // 2 = eslint's own crash / config error
	}
	res := parseResult{failing: map[string]struct{}{}, passing: map[string]struct{}{}}
	errRows := 0
	problems := -1
	current := ""
	for _, l := range lines {
		if m := reEsRow.FindStringSubmatch(l); m != nil {
			if m[1] == "error" {
				if current == "" {
					// An error row with no owning heading: the failing list
					// could no longer be vouched complete.
					return parseResult{}, false
				}
				errRows++
				res.failing[current] = struct{}{}
			}
			continue
		}
		if m := reEsProblem.FindStringSubmatch(l); m != nil {
			problems = atoiDigits(m[1])
			current = ""
			continue
		}
		// A block heading: non-indented, non-empty. Anything else (summary
		// notes, blank separators) clears the current file.
		if l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
			current = l
		} else {
			current = ""
		}
	}
	if problems < 0 {
		return parseResult{}, false // sentinel severed
	}
	if errRows != problems {
		return parseResult{}, false // a row was lost: torn output
	}
	res.cleanRun = exit == 0 && errRows == 0
	return res, true
}
