// Package delta is rundiff's pure core: given the previous run's raw output+exit
// and the current run's, it computes what changed as an order-independent diff of
// normalized lines, the exit-code transition, and — when the delta cannot be
// trusted — a degrade decision. It performs no I/O, reads no clock, and imports
// no other local package (all non-determinism — the baseline age, the cache key,
// the flags — is injected). The CLI adapts runner/cache values into these types.
//
// The order-independent (multiset) diff is the whole point: a test runner that
// prints its cases in a different order each run produces the SAME set of
// normalized lines, so a reordered-but-otherwise-identical run reports zero
// changes. A line that merely moved counts as unchanged.
package delta

import "sort"

// Run is one execution fed into the diff: the raw combined output and the exit
// code (-1 = terminated by a signal). Raw output is stored so the diff always
// re-normalizes with the current ruleset.
type Run struct {
	Output []byte
	Exit   int
}

// Options tunes a single Diff. Zero value is the safe default set (all
// default-ON normalization active, no opt-in rules, churn limit 0.5, budgets
// filled in by withDefaults).
type Options struct {
	JSON bool // emit line 1 as the whole record (arrays/body embedded), no text body
	Raw  bool // skip normalization entirely (compare raw lines)
	Full bool // show the bounded full current output as the body even when a delta exists

	// ChurnLimit degrades to full output when churn >= this. A pointer so the
	// unset case (nil) is distinguishable from an explicit 0: nil defaults to
	// 0.5, while a caller passing 0 means "degrade on any change" and is honored
	// verbatim (a plain float64 zero-value would collide with the default).
	ChurnLimit    *float64
	MaxBodyLines  int // bounded-full budget (default 200)
	MaxDeltaLines int // per-side +/- cap in a delta body (default 500)

	// Per-rule escapes (the rule is ON unless the escape is set).
	NoTime, NoDur, NoTmp, NoUUID, NoPort bool
	// Opt-in rules (default OFF; each can hide a real change).
	NormalizePtr, NormalizeHex, NormalizeDate, CollapseSpaces bool
}

func (o Options) withDefaults() Options {
	if o.ChurnLimit == nil {
		def := 0.5
		o.ChurnLimit = &def
	}
	if o.MaxBodyLines == 0 {
		o.MaxBodyLines = 200
	}
	if o.MaxDeltaLines == 0 {
		o.MaxDeltaLines = 500
	}
	return o
}

// Transition is the exit-code transition, derived from the exit pair only and so
// always trustworthy (emitted even when the line diff is degraded).
type Transition string

const (
	TransitionBaseline     Transition = "baseline"
	TransitionStillPassing Transition = "still_passing"
	TransitionStillFailing Transition = "still_failing"
	TransitionFixed        Transition = "fixed"
	TransitionRegressed    Transition = "regressed"
)

// transition maps (prevExit, curExit) to a Transition. prev == nil ⇒ baseline.
func transition(prevExit *int, curExit int) Transition {
	if prevExit == nil {
		return TransitionBaseline
	}
	prevPass := *prevExit == 0
	curPass := curExit == 0
	switch {
	case prevPass && curPass:
		return TransitionStillPassing
	case !prevPass && !curPass:
		return TransitionStillFailing
	case !prevPass && curPass:
		return TransitionFixed
	default:
		return TransitionRegressed
	}
}

// lineSet is the multiset of normalized lines for one run, with a deterministic
// (permutation-invariant) raw representative per key.
type lineSet struct {
	counts map[string]int    // normalized line → count
	rep    map[string]string // normalized line → lexicographically smallest raw line
	total  int               // total lines (every line counts, blanks included)
}

func buildSet(lines []string, n normalizer) lineSet {
	ls := lineSet{counts: make(map[string]int, len(lines)), rep: make(map[string]string, len(lines)), total: len(lines)}
	for _, raw := range lines {
		key := n.line(raw)
		if _, seen := ls.counts[key]; !seen {
			ls.rep[key] = raw
		} else if raw < ls.rep[key] {
			// Smallest raw wins → the representative does not depend on input order.
			ls.rep[key] = raw
		}
		ls.counts[key]++
	}
	return ls
}

// diffCounts is the multiset comparison. Conservation holds exactly:
// unchanged+removed == prev.total and unchanged+added == cur.total.
type diffCounts struct {
	added, removed, unchanged int
	addedLines, removedLines  []string // raw representatives, with multiplicity, sorted
}

func compare(prev, cur lineSet, maxLines int) (diffCounts, bool) {
	var dc diffCounts
	truncated := false

	// Every key that appears in either set.
	keys := make([]string, 0, len(prev.counts)+len(cur.counts))
	seen := make(map[string]struct{}, len(prev.counts)+len(cur.counts))
	for k := range prev.counts {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	for k := range cur.counts {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	for _, k := range keys {
		p := prev.counts[k]
		c := cur.counts[k]
		switch {
		case c > p:
			n := c - p
			dc.added += n
			dc.unchanged += p
			for i := 0; i < n; i++ {
				if len(dc.addedLines) < maxLines {
					dc.addedLines = append(dc.addedLines, cur.rep[k])
				} else {
					truncated = true
				}
			}
		case p > c:
			n := p - c
			dc.removed += n
			dc.unchanged += c
			for i := 0; i < n; i++ {
				if len(dc.removedLines) < maxLines {
					dc.removedLines = append(dc.removedLines, prev.rep[k])
				} else {
					truncated = true
				}
			}
		default:
			dc.unchanged += p
		}
	}
	return dc, truncated
}

func churn(added, removed, unchanged int) float64 {
	denom := added + removed + unchanged
	if denom == 0 {
		return 0
	}
	// Round to 3 decimals so the serialized value is deterministic.
	v := float64(added+removed) / float64(denom)
	return float64(int64(v*1000+0.5)) / 1000
}

// Diff computes the report of cur against prev. prev == nil ⇒ baseline (first
// run for this key). ageSeconds is now-minus-baseline-creation (already clamped
// ≥0 by the caller); key is the 12-hex cache-key prefix. Diff is pure: same
// inputs ⇒ byte-identical Report.
func Diff(prev *Run, cur Run, ageSeconds int, key string, opt Options) Report {
	opt = opt.withDefaults()
	n := newNormalizer(opt)

	r := Report{lineCore: lineCore{
		V:          schemaVersion,
		Key:        key,
		Exit:       cur.Exit,
		Normalized: !opt.Raw,
	}}

	if prev == nil {
		r.Transition = string(TransitionBaseline)
		r.body(cur.Output, opt)
		return r
	}

	prevExit := prev.Exit
	r.PrevExit = &prevExit
	age := ageSeconds
	r.BaselineAgeS = &age
	tr := transition(&prevExit, cur.Exit)
	r.Transition = string(tr)

	// Degrade checks that must run BEFORE building the multiset (counts stay
	// null; the map is never built, bounding memory).
	if reason, ok := degradePreDiff(prev.Output, cur.Output); ok {
		r.Degraded = true
		r.setReason(reason)
		r.body(cur.Output, opt)
		return r
	}

	prevSet := buildSet(splitLines(prev.Output), n)
	curSet := buildSet(splitLines(cur.Output), n)
	dc, truncated := compare(prevSet, curSet, opt.MaxDeltaLines)

	added, removed, unchanged := dc.added, dc.removed, dc.unchanged
	ch := churn(added, removed, unchanged)
	tp, tc := prevSet.total, curSet.total
	r.Added, r.Removed, r.Unchanged = &added, &removed, &unchanged
	r.Churn = &ch
	r.TotalPrev, r.TotalCur = &tp, &tc
	r.Truncated = truncated

	// Degrade checks with real counts. The already-computed churn is passed in so
	// the reported r.Churn and the G5 threshold compare the exact same number.
	if reason, ok := degradePostDiff(prevSet, curSet, len(prev.Output), len(cur.Output), dc, ch, opt); ok {
		r.Degraded = true
		r.setReason(reason)
		r.body(cur.Output, opt)
		return r
	}

	// Trusted delta. --full still shows the whole current output as the body.
	if opt.Full {
		r.body(cur.Output, opt)
		return r
	}
	r.delta(dc, opt)
	return r
}
