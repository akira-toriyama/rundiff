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
	"strings"
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
	// filter, never proof — the output decides). Hints look only at the
	// command position and one launcher level (npx/pnpm/…): a tool name in an
	// arbitrary token (an npm script named "tsc", a path argument) must NOT
	// fire, or it would narrow the candidate set away from a composite
	// ambiguity the exactly-one rule exists to refuse.
	hint(argv []string) bool
	// match reports whether the cleaned lines self-identify as this tool's
	// output, anchored on its most fossilized sentinels.
	match(lines []string) bool
	// blockedFlags reports an argv flag that changes this tool's output format
	// or exit semantics into something the parser does not cover.
	blockedFlags(argv []string) bool
	// selectionFlags reports a NAME-level test-selection flag (-run, -k, -t,
	// --onlyChanged, a cargo name filter, …). Under such a flag the identity
	// universe depends on test NAMES or git state, so a rename or an unrelated
	// edit silently deselects a still-failing test between runs with identical
	// argv — pass evidence then proves nothing, and the cross-run pair is
	// withheld. Path-level selection (jest path regexes, pytest dirs) stays
	// allowed: identities ARE paths, so a moved path simply loses its evidence
	// and A7 nils the pair on its own.
	selectionFlags(argv []string) bool
	// silentWhenClean marks tools whose clean run legitimately prints nothing
	// (tsc, eslint) — eligible for silent-clean adoption in Extract and the
	// only tools whose cleanRun feeds the global pass proof.
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

// parserEnv maps a tool to the environment variables whose contents change ITS
// behavior (selection/format). The CLI can inject flags through these without
// touching argv or the cache key, so the gates must see them — but ONLY for the
// owning tool: GOFLAGS is Go's, PYTEST_ADDOPTS is pytest's, and a Go env var
// must never withhold a pytest claim (or vice versa).
var parserEnv = map[string][]string{
	"go-test": {"GOFLAGS"},
	"pytest":  {"PYTEST_ADDOPTS"},
}

// EnvVarNames is the union of every var in parserEnv, sorted. The CLI reads
// exactly these from the environment, so the two lists cannot drift: adding a
// var to parserEnv automatically makes the CLI pass it.
func EnvVarNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, names := range parserEnv {
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

// gateArgs is what a parser's blockedFlags/selectionFlags see: the tool's own
// argument tokens (launcher stripped) plus the tokens of the env vars that
// tool owns. env is the raw environment (var name → value); nil is fine.
func gateArgs(argv []string, env map[string]string, p parser) []string {
	_, toolArgs := resolveTool(argv)
	out := append([]string{}, toolArgs...)
	for _, name := range parserEnv[p.name()] {
		out = append(out, strings.Fields(env[name])...)
	}
	return out
}

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
// env is the raw environment (var name → value); the gates read the tokens of
// each tool's OWN vars (GOFLAGS for go, PYTEST_ADDOPTS for pytest) so an
// env-injected -run/-k reaches them even though it never touches argv or the
// cache key. forceTool: "" = auto-detect, "none" = disabled, else a Tools()
// name — forcing selects a parser, it never bypasses a gate (unknown names are
// the CLI's to reject; here they simply select nothing).
func Extract(argv []string, env map[string]string, prev *Run, cur Run, forceTool string) *Claim {
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

	curR := resolveRun(argv, env, cur, forced)
	var prevR resolution
	if prev != nil {
		prevR = resolveRun(argv, env, *prev, forced)
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

	// Name-level selection (-run, -k, -t, --onlyChanged, a cargo filter, in
	// argv or the environment): the identity universe then depends on test
	// names or git state, so a rename or an unrelated edit silently deselects
	// a still-failing test between runs with identical argv — a green subset
	// run would read as a fix. Pass evidence proves nothing here; withhold the
	// pair (failing, from lines actually printed, stays sound). Checked over
	// the current argv+env; the baseline's env is unobservable (not cached), so
	// a selection dropped between runs can still yield a false `new` — the safe
	// direction (New ⊆ Failing: the identity genuinely fails now).
	if commandOpaque(argv) || curR.p.selectionFlags(gateArgs(argv, env, curR.p)) {
		return claim
	}

	// A7 strict accounting: every previously-failing identity must either
	// still be failing, or carry positive pass evidence. For chatty tools that
	// is PER-IDENTITY only (ok pkg, PASS file, an all-dots progress line, test
	// x ... ok): their identity universe varies with selection, skips and
	// config, so a green run does not vouch for an identity that printed no
	// line. Only silentWhenClean tools (tsc, eslint) get the global clean-run
	// proof — their clean run is inherently whole-project and markerless. An
	// identity the tool reported as not-run (skip / no tests to run) is
	// unaccounted no matter what: not running a failure is not fixing it. One
	// unaccounted identity withholds the whole pair.
	globalPass := cur.Exit == 0 && curR.res.cleanRun && curR.p.silentWhenClean()
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
// selection + exactly-one match (A2), blocked flags (A3, over the tool's own
// args plus its env), parse + reconcile (A4, inside the parser), and the
// generic exit/size cross-checks (A5).
func resolveRun(argv []string, env map[string]string, r Run, forced parser) resolution {
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
	if matched == nil || matched.blockedFlags(gateArgs(argv, env, matched)) {
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
