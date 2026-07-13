package adapter

import (
	"regexp"
	"strings"
)

// cargo test. Identity = the TEST NAME (`module::case` — rust's libtest has no
// per-file notion in its output; a doc-test's ` (line N)` suffix is position
// chrome and is stripped). The per-test verdict lines are cross-checked against
// the `failures:` block, and the failed counts summed across every
// `test result:` line (one per test binary) must match — libtest's format is a
// compatibility promise, the most fossilized of the seven.
//
//	running 3 tests
//	test tests::test_mul ... FAILED
//	failures:
//	    tests::test_mul
//	test result: FAILED. 2 passed; 1 failed; 0 ignored; ...
//
// A compile error never reaches `running N tests` ⇒ no match ⇒ no claim.
var (
	reCargoRunning = regexp.MustCompile(`^running \d+ tests?$`)
	reCargoResult  = regexp.MustCompile(`^test result: (ok|FAILED)\. .*?(\d+) passed; (\d+) failed;`)
	reCargoVerdict = regexp.MustCompile(`^test (.+?) \.\.\. (ok|FAILED|ignored(?:, .*)?)$`) // rust ≥1.61: "ignored, <reason>"
	reCargoFailRef = regexp.MustCompile(`^    (\S.*)$`)                                     // rows of a failures: block
	reCargoDocLine = regexp.MustCompile(` \(line \d+\)$`)
)

type cargoTest struct{}

func init() { register(cargoTest{}) }

func (cargoTest) name() string { return "cargo-test" }

func (cargoTest) hint(argv []string) bool {
	return invokes(argv, "cargo") && hasWord(argv, "test", "t") // `t` is cargo's built-in test alias
}

func (cargoTest) match(lines []string) bool {
	running, result := false, false
	for _, l := range lines {
		if reCargoRunning.MatchString(l) {
			running = true
		}
		if reCargoResult.MatchString(l) {
			result = true
		}
	}
	return running && result
}

func (cargoTest) blockedFlags(argv []string) bool {
	return hasFlag(argv, "--format", "--message-format", "-q", "--quiet") || hasFlagPrefix(argv, "-Z")
}

// cargoValueFlags consume the following token as a value — that token is a
// package/target/feature selector (package-LEVEL, safe: identities are names,
// which those don't filter), not a test-NAME filter, so its value must not be
// mistaken for a positional name filter.
var cargoValueFlags = map[string]bool{
	"-p": true, "--package": true, "--features": true, "--target": true,
	"--manifest-path": true, "-j": true, "--jobs": true, "--test": true,
	"--bin": true, "--example": true, "--bench": true, "--exclude": true,
	"--color": true,
}

func (cargoTest) selectionFlags(argv []string) bool {
	// A test-NAME filter (a rename escapes it) is: a bare positional after the
	// `test`/`t` subcommand and before `--`, or any libtest arg after `--`
	// (a bare name, or --exact/--skip). Package/feature selection (-p, --features)
	// is package-level and stays allowed.
	i := 0
	for ; i < len(argv); i++ {
		if argv[i] == "test" || argv[i] == "t" {
			i++
			break
		}
	}
	afterDD := false
	for ; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--":
			afterDD = true
		case afterDD:
			if a == "--exact" || a == "--skip" || !strings.HasPrefix(a, "-") {
				return true // a name filter (or its target) after --
			}
		case cargoValueFlags[a]:
			i++ // skip the value: package/target/feature, not a name
		case strings.HasPrefix(a, "-"):
			// a glued/boolean flag (-p=…, --workspace, --release): not a name
		default:
			return true // a bare positional before -- is a name filter
		}
	}
	return false
}

func (cargoTest) silentWhenClean() bool { return false }

// Identity IS the test name — the selection unit itself — so a filtered-out test
// simply loses its line.
func (cargoTest) pairNeedsHint() bool { return false }

func (cargoTest) parse(lines []string, exit int) (parseResult, bool) {
	if exit != 0 && exit != 101 {
		return parseResult{}, false
	}
	res := parseResult{failing: map[string]struct{}{}, passing: map[string]struct{}{}, notRun: map[string]struct{}{}}
	failRefs := map[string]struct{}{} // names under failures: blocks
	sumFailed, sumPassed := 0, 0
	sawResult := false
	inFailures := false
	seen := map[string]struct{}{} // verdicts per name, across all test binaries
	for _, l := range lines {
		if m := reCargoVerdict.FindStringSubmatch(l); m != nil {
			name := reCargoDocLine.ReplaceAllString(m[1], "")
			// The same unqualified name in two binaries (workspace crates both
			// defining tests::x) would merge two different tests into one
			// identity — a fail-swap between them would read as "no change".
			if _, dup := seen[name]; dup {
				return parseResult{}, false
			}
			seen[name] = struct{}{}
			switch m[2] {
			case "FAILED":
				res.failing[name] = struct{}{}
			case "ok":
				res.passing[name] = struct{}{}
			default: // ignored / ignored, <reason>
				res.notRun[name] = struct{}{}
			}
			inFailures = false
			continue
		}
		if m := reCargoResult.FindStringSubmatch(l); m != nil {
			sawResult = true
			sumPassed += atoiDigits(m[2])
			sumFailed += atoiDigits(m[3])
			inFailures = false
			continue
		}
		if strings.TrimSpace(l) == "failures:" {
			inFailures = true
			continue
		}
		if inFailures {
			if m := reCargoFailRef.FindStringSubmatch(l); m != nil {
				failRefs[reCargoDocLine.ReplaceAllString(m[1], "")] = struct{}{}
			} else if strings.TrimSpace(l) != "" {
				inFailures = false
			}
		}
	}
	if !sawResult {
		return parseResult{}, false
	}
	if sumFailed != len(res.failing) {
		return parseResult{}, false // a FAILED verdict line was lost: torn output
	}
	// The failures: blocks re-list every failed test; a mismatch means the
	// verdict lines and the summary disagree about identity, not just count.
	if sumFailed > 0 && !sameKeys(failRefs, res.failing) {
		return parseResult{}, false
	}
	res.cleanRun = exit == 0 && sumFailed == 0 && sumPassed > 0
	return res, true
}

func sameKeys(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}
