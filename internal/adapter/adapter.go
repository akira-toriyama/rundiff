// Package adapter is rundiff's second pure leaf: it recognizes the output of
// common dev tools (go test, pytest, jest, vitest, cargo test, tsc, eslint),
// extracts each run's set of FAILING identities (file paths, or package paths /
// test names where the tool has no file notion), and turns a run pair into a
// file-level claim — fixed / new — for the JSON line. Like internal/delta it
// performs no I/O, reads no clock, imports no other local package, and never
// panics on arbitrary bytes; the CLI feeds it values.
//
// The safety stance inverts delta's. The line diff degrades — it shows MORE
// when unsure. A claim cannot degrade; its failure mode is a false statement,
// and a false fixed:["x"] makes an agent stop looking — the worst outcome this
// tool can produce. So the adapter's bias is: WHEN UNSURE, SAY NOTHING.
// Three rules follow:
//
//  1. nil ≠ empty. A nil Claim (or nil Fixed/New) is "no claim"; an empty
//     slice is "parsed completely and confidently: nothing".
//  2. All-or-nothing accounting. A cross-run claim is a complete account: if
//     even one previously-failing identity's fate cannot be proven, Fixed and
//     New are withheld together.
//  3. Positive evidence for fixed. "Fixed" is never inferred from absence.
//     It needs per-identity pass evidence (ok pkg, PASS file, …) or a global
//     clean-run proof (exit 0 plus the tool's own zero-failure output). This
//     structurally neutralizes bail/fail-fast, varying selection (--lf,
//     --onlyChanged, shards), renames and truncation: the identity becomes
//     unaccounted, which nils the pair.
package adapter

import (
	"bytes"
	"sort"
)

// Guards duplicated from delta's G1/G2 constants (leaf rule: no shared import),
// plus the claim-size cap: fixed/new/failing are truth claims, not display
// arrays — a clipped claim invites "is X in the hidden part?", so an oversize
// set nils the whole claim instead of truncating it.
const (
	maxInputBytes = 8 << 20
	maxInputLines = 50_000
	maxIdentities = 200
)

// Run is one execution's raw combined output and exit code (-1 = terminated by
// a signal). It mirrors delta.Run by value, not by import — both packages are
// import-free leaves.
type Run struct {
	Output []byte
	Exit   int
}

// Claim is what the adapter is willing to assert. Failing is the current run's
// complete failing identities (non-nil whenever Claim is non-nil). Fixed/New
// are the cross-run claim — nil or non-nil together; nil means "no claim",
// empty means "confidently none". Slices are sorted and deduped;
// Fixed ∩ New = ∅, New ⊆ Failing, Fixed ∩ Failing = ∅.
type Claim struct {
	Tool    string
	Failing []string
	Fixed   []string
	New     []string
}

// parser is the per-tool extension seam. Implementations are pure and total:
// parse never panics on arbitrary lines and returns ok=false whenever the run
// cannot be vouched for (missing sentinel, count mismatch, unexpected exit).
type parser interface {
	name() string
	// hint reports whether argv plausibly invokes this tool (a candidate
	// filter, never proof — the output decides).
	hint(argv []string) bool
	// match reports whether the cleaned lines self-identify as this tool's
	// output, anchored on its most fossilized sentinels.
	match(lines []string) bool
	// blockedFlags reports an argv flag that changes this tool's output format
	// or exit semantics into something the parser does not cover.
	blockedFlags(argv []string) bool
	// silentWhenClean marks tools whose clean run legitimately prints nothing
	// (tsc, eslint) — eligible for silent-clean adoption in Extract.
	silentWhenClean() bool
	// parse extracts the failure/pass evidence from one run. ok=false ⇒ this
	// run makes no claim at all.
	parse(lines []string, exit int) (parseResult, bool)
}

type parseResult struct {
	failing map[string]struct{} // identities with positive failure evidence
	passing map[string]struct{} // identities with positive pass evidence
	// notRun holds identities the tool positively reported as NOT executed
	// (go's `? pkg [no test files]`, a fully-skipped pytest file). Not running
	// a failure is not fixing it, so notRun blocks the global clean-run proof
	// for that identity — per-identity and global evidence both lose to it.
	notRun map[string]struct{}
	// cleanRun is the tool's own global zero-failure evidence (e.g. an ok
	// trailer with no FAIL, "N passed" with no failed) — the global pass proof
	// that lets a fixed claim cover identities lacking per-identity evidence.
	cleanRun bool
}

// parsers is the registry, filled by each tool file's init(). The exactly-one
// match rule makes registration order irrelevant.
var parsers []parser

func register(p parser) { parsers = append(parsers, p) }

// Tools returns the registered parser names, sorted (for --tool validation).
func Tools() []string {
	names := make([]string, 0, len(parsers))
	for _, p := range parsers {
		names = append(names, p.name())
	}
	sort.Strings(names)
	return names
}

// resolution is one run's outcome: parsed under exactly one parser (ok), a
// silent-clean candidate (cleanEmpty — adoptable by the other run's tool), or
// nothing.
type resolution struct {
	p          parser
	res        parseResult
	ok         bool
	cleanEmpty bool
}

// Extract parses both runs and returns a Claim, or nil when the current run
// cannot be confidently parsed. prev == nil ⇒ baseline (Fixed/New stay nil).
// forceTool: "" = auto-detect, "none" = disabled, else a Tools() name —
// forcing selects a parser, it never bypasses a gate (unknown names are the
// CLI's to reject; here they simply select nothing).
func Extract(argv []string, prev *Run, cur Run, forceTool string) *Claim {
	if forceTool == "none" {
		return nil
	}
	var forced parser
	if forceTool != "" {
		for _, p := range parsers {
			if p.name() == forceTool {
				forced = p
			}
		}
		if forced == nil {
			return nil
		}
	}

	curR := resolveRun(argv, cur, forced)
	var prevR resolution
	if prev != nil {
		prevR = resolveRun(argv, *prev, forced)
	}

	// Silent-clean adoption: a run with exit 0 and blank output is claimable by
	// a silentWhenClean tool (tsc, eslint print nothing on success) when that
	// tool was selected by the OTHER run's match plus an agreeing argv hint, or
	// forced. Both runs blank ⇒ neither side has output evidence ⇒ nil.
	adopt := func(other resolution) (resolution, bool) {
		if other.ok && other.p.silentWhenClean() && (forced != nil || other.p.hint(argv)) {
			return resolution{
				p: other.p,
				res: parseResult{
					failing:  map[string]struct{}{},
					passing:  map[string]struct{}{},
					cleanRun: true,
				},
				ok: true,
			}, true
		}
		return resolution{}, false
	}
	if curR.cleanEmpty && prev != nil {
		if r, ok := adopt(prevR); ok {
			curR = r
		}
	}
	if prev != nil && prevR.cleanEmpty {
		if r, ok := adopt(curR); ok {
			prevR = r
		}
	}

	if !curR.ok {
		return nil
	}

	claim := &Claim{Tool: curR.p.name(), Failing: sortedKeys(curR.res.failing)}

	// Pair gates. A6 comparability: a baseline, an unparsed previous run, or a
	// different tool ⇒ no cross-run claim (Failing survives).
	if prev == nil || !prevR.ok || prevR.p.name() != curR.p.name() {
		return claim
	}

	// A7 strict accounting: every previously-failing identity must either still
	// be failing, or carry positive pass evidence — per-identity, or the global
	// clean-run proof (exit 0 + the tool's own zero-failure output). An
	// identity the tool reported as not-run (deletion / skip) is unaccounted no
	// matter what: not running a failure is not fixing it. One unaccounted
	// identity withholds the whole pair.
	globalPass := cur.Exit == 0 && curR.res.cleanRun
	for id := range prevR.res.failing {
		if _, skipped := curR.res.notRun[id]; skipped {
			return claim
		}
		if _, still := curR.res.failing[id]; still {
			continue
		}
		if _, passed := curR.res.passing[id]; passed {
			continue
		}
		if globalPass {
			continue
		}
		return claim
	}

	claim.Fixed = diffKeys(prevR.res.failing, curR.res.failing)
	claim.New = diffKeys(curR.res.failing, prevR.res.failing)
	return claim
}

// resolveRun runs the per-run gate pipeline: input guards (A1), candidate
// selection + exactly-one match (A2), blocked flags (A3), parse + reconcile
// (A4, inside the parser), and the generic exit/size cross-checks (A5).
func resolveRun(argv []string, r Run, forced parser) resolution {
	if len(r.Output) > maxInputBytes || bytes.IndexByte(r.Output, 0) >= 0 {
		return resolution{}
	}
	lines := cleanLines(r.Output)
	if len(lines) > maxInputLines {
		return resolution{}
	}
	if r.Exit == 0 && allBlank(lines) {
		return resolution{cleanEmpty: true}
	}

	candidates := parsers
	if forced != nil {
		candidates = []parser{forced}
	} else if hinted := hintCandidates(argv); len(hinted) > 0 {
		candidates = hinted
	}

	var matched parser
	for _, p := range candidates {
		if !p.match(lines) {
			continue
		}
		if matched != nil {
			// Two tools' shapes in one output (a composite script): ambiguity
			// is a safety event, not a priority contest.
			return resolution{}
		}
		matched = p
	}
	if matched == nil || matched.blockedFlags(argv) {
		return resolution{}
	}

	res, ok := matched.parse(lines, r.Exit)
	if !ok {
		return resolution{}
	}
	if r.Exit < 0 {
		return resolution{}
	}
	if (r.Exit == 0) != (len(res.failing) == 0) {
		return resolution{}
	}
	if len(res.failing) > maxIdentities {
		return resolution{}
	}
	return resolution{p: matched, res: res, ok: true}
}

// hintCandidates returns the parsers whose argv hint fires. Empty ⇒ the caller
// should treat every parser as a candidate (npm test / make check hide the real
// tool; the output fingerprint decides either way).
func hintCandidates(argv []string) []parser {
	var out []parser
	for _, p := range parsers {
		if p.hint(argv) {
			out = append(out, p)
		}
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// diffKeys returns a ∖ b, sorted.
func diffKeys(a, b map[string]struct{}) []string {
	out := []string{}
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
