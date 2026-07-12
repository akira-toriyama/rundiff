package delta

import (
	"strings"
	"testing"
)

func claimMeta(c *FileClaim) Meta { return Meta{Key: "k", FileClaim: c} }

// The four claim fields must be null — not [] — when no claim was injected, in
// every render mode. null = "no claim" is the contract; a coerced [] would read
// as "confidently nothing failing".
func TestClaim_absentIsNull(t *testing.T) {
	prev := bigRun(1, "a")
	cur := bigRun(1, "b")
	for _, opt := range []Options{{JSON: true}, {JSON: true, Full: true}} {
		r := Diff(&prev, cur, claimMeta(nil), opt)
		line, _ := Render(r, opt)
		for _, want := range []string{`"tool":null`, `"failing":null`, `"fixed":null`, `"new":null`} {
			if !strings.Contains(string(line), want) {
				t.Errorf("opt=%+v: line missing %s: %s", opt, want, line)
			}
		}
	}
}

// A failing-only claim (no cross-run pair) keeps fixed/new null while tool and
// failing are set; an empty Failing renders as [], not null.
func TestClaim_failingOnly(t *testing.T) {
	prev := bigRun(1, "a")
	cur := bigRun(1, "b")
	c := &FileClaim{Tool: "pytest", Failing: []string{"tests/test_x.py"}}
	r := Diff(&prev, cur, claimMeta(c), Options{JSON: true})
	line, _ := Render(r, Options{JSON: true})
	for _, want := range []string{`"tool":"pytest"`, `"failing":["tests/test_x.py"]`, `"fixed":null`, `"new":null`} {
		if !strings.Contains(string(line), want) {
			t.Errorf("line missing %s: %s", want, line)
		}
	}

	empty := &FileClaim{Tool: "jest", Failing: []string{}, Fixed: []string{}, New: []string{}}
	r = Diff(&prev, cur, claimMeta(empty), Options{JSON: true})
	line, _ = Render(r, Options{JSON: true})
	for _, want := range []string{`"tool":"jest"`, `"failing":[]`, `"fixed":[]`, `"new":[]`} {
		if !strings.Contains(string(line), want) {
			t.Errorf("line missing %s: %s", want, line)
		}
	}
}

// The claim is defensively sorted and deduped, the caller's slices are never
// mutated, and a lopsided Fixed/New pair collapses to null together.
func TestClaim_defensiveNormalization(t *testing.T) {
	prev := bigRun(1, "a")
	cur := bigRun(1, "b")
	failing := []string{"z.py", "a.py", "z.py"}
	c := &FileClaim{Tool: "pytest", Failing: failing, Fixed: []string{"b.py", "a.py"}, New: []string{"z.py"}}
	r := Diff(&prev, cur, claimMeta(c), Options{JSON: true})
	line, _ := Render(r, Options{JSON: true})
	if !strings.Contains(string(line), `"failing":["a.py","z.py"]`) {
		t.Errorf("failing not sorted+deduped: %s", line)
	}
	if !strings.Contains(string(line), `"fixed":["a.py","b.py"]`) {
		t.Errorf("fixed not sorted: %s", line)
	}
	if failing[0] != "z.py" || failing[1] != "a.py" {
		t.Errorf("caller slice mutated: %v", failing)
	}

	lop := &FileClaim{Tool: "jest", Failing: []string{"x"}, Fixed: []string{"y"}} // New nil
	r = Diff(&prev, cur, claimMeta(lop), Options{JSON: true})
	line, _ = Render(r, Options{JSON: true})
	if !strings.Contains(string(line), `"fixed":null`) || !strings.Contains(string(line), `"new":null`) {
		t.Errorf("lopsided pair must collapse to null together: %s", line)
	}
}

// A baseline may carry tool+failing but never a cross-run claim.
func TestClaim_baselineDropsCrossRun(t *testing.T) {
	cur := bigRun(1, "b")
	c := &FileClaim{Tool: "go-test", Failing: []string{"pkg/a"}, Fixed: []string{"pkg/b"}, New: []string{"pkg/a"}}
	r := Diff(nil, cur, claimMeta(c), Options{JSON: true})
	line, _ := Render(r, Options{JSON: true})
	for _, want := range []string{`"tool":"go-test"`, `"failing":["pkg/a"]`, `"fixed":null`, `"new":null`} {
		if !strings.Contains(string(line), want) {
			t.Errorf("baseline line missing %s: %s", want, line)
		}
	}
}

// Text mode: non-empty fixed/new render as compact lines between the header and
// the delta body; empty claims render nothing extra.
func TestClaim_textDeltaLines(t *testing.T) {
	prev := bigRun(1, "keep", "gone")
	cur := bigRun(1, "keep", "fresh")
	c := &FileClaim{Tool: "jest", Failing: []string{"b.test.ts"}, Fixed: []string{"a.test.ts"}, New: []string{"b.test.ts"}}
	r := Diff(&prev, cur, claimMeta(c), Options{})
	_, body := Render(r, Options{})
	lines := strings.Split(body, "\n")
	if len(lines) < 3 || lines[1] != "fixed (jest): a.test.ts" || lines[2] != "new (jest): b.test.ts" {
		t.Errorf("claim lines not rendered after header:\n%s", body)
	}
	if !strings.Contains(body, "- gone") || !strings.Contains(body, "+ fresh") {
		t.Errorf("delta body lost its diff lines:\n%s", body)
	}

	quiet := &FileClaim{Tool: "jest", Failing: []string{}, Fixed: []string{}, New: []string{}}
	r = Diff(&prev, cur, claimMeta(quiet), Options{})
	_, body = Render(r, Options{})
	if strings.Contains(body, "fixed (") || strings.Contains(body, "new (") {
		t.Errorf("empty claim must render no extra lines:\n%s", body)
	}
}

// Text mode: a full body (baseline / degrade / --full) leads with the failing
// list when it is non-empty — the run just lost its detail view.
func TestClaim_textFailingLineOnFullBody(t *testing.T) {
	c := &FileClaim{Tool: "pytest", Failing: []string{"tests/test_a.py", "tests/test_b.py"}}

	cur := bigRun(1, "b")
	r := Diff(nil, cur, claimMeta(c), Options{})
	_, body := Render(r, Options{})
	lines := strings.Split(body, "\n")
	if len(lines) < 2 || lines[1] != "failing (pytest): tests/test_a.py, tests/test_b.py" {
		t.Errorf("baseline body missing failing line:\n%s", body)
	}

	prev := bigRun(1, "a")
	r = Diff(&prev, cur, claimMeta(c), Options{ChurnLimit: fptr(0)}) // force a degrade
	if !r.Degraded {
		t.Fatal("expected a degraded diff")
	}
	_, body = Render(r, Options{})
	lines = strings.Split(body, "\n")
	if len(lines) < 2 || lines[1] != "failing (pytest): tests/test_a.py, tests/test_b.py" {
		t.Errorf("degraded body missing failing line:\n%s", body)
	}
}
