package delta

import (
	"fmt"
	"strings"
	"testing"
)

// run builds a Run from lines (each gets a trailing newline).
func run(exit int, lines ...string) Run {
	return Run{Exit: exit, Output: []byte(strings.Join(lines, "\n") + "\n")}
}

// bigRun pads with identical filler so the output clears the small-output
// degrade threshold, letting the trusted-delta path (arrays/body) be exercised.
func bigRun(exit int, lines ...string) Run {
	padded := append([]string{}, lines...)
	for i := 0; len(strings.Join(padded, "\n")) <= minSmallBytes+64 || len(padded) < minSmallLines+1; i++ {
		padded = append(padded, fmt.Sprintf("filler line number %05d stays constant", i))
	}
	return Run{Exit: exit, Output: []byte(strings.Join(padded, "\n") + "\n")}
}

func TestTransition_matrix(t *testing.T) {
	cases := []struct {
		prevExit, curExit int
		want              Transition
	}{
		{0, 0, TransitionStillPassing},
		{1, 1, TransitionStillFailing},
		{2, 1, TransitionStillFailing}, // magnitude ignored
		{1, 0, TransitionFixed},
		{0, 1, TransitionRegressed},
		{0, -1, TransitionRegressed}, // signal death is a failure
	}
	for _, c := range cases {
		prev := run(c.prevExit, "a", "b", "c")
		cur := run(c.curExit, "a", "b", "c")
		got := Diff(&prev, cur, 0, "k", Options{})
		if got.Transition != string(c.want) {
			t.Errorf("prev=%d cur=%d: transition=%s want=%s", c.prevExit, c.curExit, got.Transition, c.want)
		}
	}
}

func TestBaseline_nilPrev(t *testing.T) {
	cur := run(0, "hello", "world", "third")
	r := Diff(nil, cur, 0, "k", Options{})
	if r.Transition != string(TransitionBaseline) {
		t.Errorf("transition=%s want baseline", r.Transition)
	}
	if r.Degraded {
		t.Error("baseline must not be degraded")
	}
	if r.DegradeReason != nil {
		t.Errorf("baseline degrade_reason=%v want nil", *r.DegradeReason)
	}
	for name, p := range map[string]*int{"added": r.Added, "removed": r.Removed, "unchanged": r.Unchanged, "baseline_age_s": r.BaselineAgeS} {
		if p != nil {
			t.Errorf("baseline %s=%d want nil", name, *p)
		}
	}
	if r.PrevExit != nil {
		t.Errorf("baseline prev_exit=%d want nil", *r.PrevExit)
	}
}

func TestIdentity_zeroDelta(t *testing.T) {
	out := bigRun(0, "line one", "line two", "line three")
	prev := out
	r := Diff(&prev, out, 5, "k", Options{})
	if r.Transition != string(TransitionStillPassing) {
		t.Errorf("transition=%s want still_passing", r.Transition)
	}
	if r.Degraded {
		t.Error("identical large output should not degrade")
	}
	if *r.Added != 0 || *r.Removed != 0 {
		t.Errorf("added=%d removed=%d want 0/0", *r.Added, *r.Removed)
	}
	if *r.Churn != 0 {
		t.Errorf("churn=%v want 0", *r.Churn)
	}
	if *r.Unchanged != *r.TotalCur {
		t.Errorf("unchanged=%d total_cur=%d want equal", *r.Unchanged, *r.TotalCur)
	}
}

func TestConservation(t *testing.T) {
	prev := bigRun(1, "keep1", "keep2", "gone1", "gone2")
	cur := bigRun(1, "keep1", "keep2", "new1")
	r := Diff(&prev, cur, 0, "k", Options{})
	if *r.Unchanged+*r.Removed != *r.TotalPrev {
		t.Errorf("unchanged(%d)+removed(%d) != total_prev(%d)", *r.Unchanged, *r.Removed, *r.TotalPrev)
	}
	if *r.Unchanged+*r.Added != *r.TotalCur {
		t.Errorf("unchanged(%d)+added(%d) != total_cur(%d)", *r.Unchanged, *r.Added, *r.TotalCur)
	}
}

func TestMultiset_duplicates(t *testing.T) {
	// "dup" appears 3× in prev, 5× in cur → unchanged 3, added 2, removed 0.
	prev := bigRun(0, "dup", "dup", "dup")
	cur := bigRun(0, "dup", "dup", "dup", "dup", "dup")
	r := Diff(&prev, cur, 0, "k", Options{})
	// The filler is identical, so isolate the "dup" contribution via totals.
	if *r.Added != 2 {
		t.Errorf("added=%d want 2 (3→5 duplicates)", *r.Added)
	}
	if *r.Removed != 0 {
		t.Errorf("removed=%d want 0", *r.Removed)
	}
}

func TestOrderIndependence_counts(t *testing.T) {
	prev := bigRun(1, "alpha", "beta", "gamma")
	// Same lines, permuted, plus one real change (gamma→delta).
	curA := bigRun(1, "alpha", "beta", "delta")
	curB := bigRun(1, "delta", "beta", "alpha")
	ra := Diff(&prev, curA, 0, "k", Options{})
	rb := Diff(&prev, curB, 0, "k", Options{})
	if *ra.Added != *rb.Added || *ra.Removed != *rb.Removed || *ra.Unchanged != *rb.Unchanged {
		t.Errorf("permutation changed counts: A(+%d -%d ~%d) B(+%d -%d ~%d)",
			*ra.Added, *ra.Removed, *ra.Unchanged, *rb.Added, *rb.Removed, *rb.Unchanged)
	}
	if *ra.Added != 1 || *ra.Removed != 1 {
		t.Errorf("added=%d removed=%d want 1/1 (gamma→delta)", *ra.Added, *ra.Removed)
	}
}

func TestOrderIndependence_renderedBodyStable(t *testing.T) {
	prev := bigRun(1, "alpha", "beta", "gamma")
	curA := Diff(&prev, bigRun(1, "delta", "epsilon", "beta", "alpha", "gamma"), 0, "k", Options{JSON: true})
	curB := Diff(&prev, bigRun(1, "gamma", "alpha", "epsilon", "beta", "delta"), 0, "k", Options{JSON: true})
	lineA, _ := Render(curA, Options{JSON: true})
	lineB, _ := Render(curB, Options{JSON: true})
	if string(lineA) != string(lineB) {
		t.Errorf("permutation changed rendered output:\nA=%s\nB=%s", lineA, lineB)
	}
}

func TestSymmetry(t *testing.T) {
	a := bigRun(0, "x", "y", "z")
	b := bigRun(0, "x", "w")
	ab := Diff(&a, b, 0, "k", Options{})
	ba := Diff(&b, a, 0, "k", Options{})
	if *ab.Added != *ba.Removed || *ab.Removed != *ba.Added || *ab.Unchanged != *ba.Unchanged {
		t.Errorf("not symmetric: ab(+%d -%d ~%d) ba(+%d -%d ~%d)",
			*ab.Added, *ab.Removed, *ab.Unchanged, *ba.Added, *ba.Removed, *ba.Unchanged)
	}
}

func TestDeterminism_sameInputSameBytes(t *testing.T) {
	prev := bigRun(0, "one", "two")
	cur := bigRun(1, "one", "three")
	var first string
	for i := 0; i < 20; i++ {
		r := Diff(&prev, cur, 7, "abc123", Options{JSON: true})
		line, _ := Render(r, Options{JSON: true})
		if i == 0 {
			first = string(line)
			continue
		}
		if string(line) != first {
			t.Fatalf("non-deterministic output on run %d:\nfirst=%s\ngot  =%s", i, first, line)
		}
	}
}

func TestBaselineAge_clampedByCaller_passthrough(t *testing.T) {
	prev := run(0, "a", "b", "c")
	cur := run(0, "a", "b", "c")
	r := Diff(&prev, cur, 42, "k", Options{})
	if r.BaselineAgeS == nil || *r.BaselineAgeS != 42 {
		t.Errorf("baseline_age_s = %v want 42", r.BaselineAgeS)
	}
}
