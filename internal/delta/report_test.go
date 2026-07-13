package delta

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBoundedFull_truncatesWithMarker(t *testing.T) {
	// A baseline over MaxBodyLines (default 200) must head+tail truncate: 50 head
	// lines, a "[... N lines omitted ...]" marker, then budget-50 tail lines, with
	// Report.Truncated set. This is the bounded-full view for baseline/degrade/--full.
	lines := make([]string, 300)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %03d", i)
	}
	out := []byte(strings.Join(lines, "\n") + "\n")

	r := Diff(nil, Run{Output: out, Exit: 0}, Meta{Key: "k"}, Options{})
	if !r.Truncated {
		t.Error("a 300-line baseline (> MaxBodyLines) should set Truncated")
	}
	_, body := Render(r, Options{})
	if !strings.Contains(body, "[... 100 lines omitted ...]") {
		t.Errorf("body missing omission marker; first 120 bytes:\n%.120s", body)
	}
	// header + 50 head + marker + 150 tail = 201 content lines, each on its own
	// line after the header ⇒ 201 newlines.
	if n := strings.Count(body, "\n"); n != 201 {
		t.Errorf("body newline count = %d, want 201 (header + 50 head + marker + 150 tail)", n)
	}
	// Tail bias: the last real line must be present (failures cluster at the end).
	if !strings.Contains(body, "line 299") {
		t.Error("tail-biased body should include the last line (line 299)")
	}
}

func TestDelta_maxDeltaLinesCapsArraysButCountsStayTrue(t *testing.T) {
	// A trusted (non-degraded) delta whose changed lines exceed MaxDeltaLines must
	// cap the emitted arrays and set Truncated, while the counts still report the
	// TRUE totals — so a consumer knows the arrays are a sample, not the whole set.
	shared := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		shared = append(shared, fmt.Sprintf("shared line %05d constant filler xyz", i))
	}
	prevLines := append(append([]string{}, shared...), "old-a", "old-b", "old-c", "old-d", "old-e")
	curLines := append(append([]string{}, shared...), "new-a", "new-b", "new-c", "new-d", "new-e")
	prev := Run{Output: []byte(strings.Join(prevLines, "\n") + "\n"), Exit: 0}
	cur := Run{Output: []byte(strings.Join(curLines, "\n") + "\n"), Exit: 0}

	r := Diff(&prev, cur, Meta{Key: "k"}, Options{MaxDeltaLines: 2, JSON: true})
	if r.Degraded {
		t.Fatalf("should be a trusted delta, got degraded (reason=%v)", *r.DegradeReason)
	}
	if !r.Truncated {
		t.Error("a delta exceeding MaxDeltaLines should set Truncated")
	}
	if *r.Added != 5 || *r.Removed != 5 {
		t.Errorf("counts should report true totals: added=%d removed=%d want 5/5", *r.Added, *r.Removed)
	}
	line, _ := Render(r, Options{JSON: true})
	var got struct {
		Truncated    bool     `json:"truncated"`
		AddedLines   []string `json:"added_lines"`
		RemovedLines []string `json:"removed_lines"`
	}
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatalf("render JSON invalid: %v", err)
	}
	if len(got.AddedLines) != 2 || len(got.RemovedLines) != 2 {
		t.Errorf("arrays should be capped at MaxDeltaLines=2: len(added)=%d len(removed)=%d",
			len(got.AddedLines), len(got.RemovedLines))
	}
	if !got.Truncated {
		t.Errorf("rendered JSON should carry truncated:true, got line: %s", line)
	}
}

// The claim is the most valuable thing rundiff produces, and the degraded body
// is where most real runs land (G4 fires whenever both outputs are small — most
// suites are). It used to appear on line 1 only, i.e. nowhere a reader of the
// body would find it.
func TestBody_degradedBodyCarriesTheClaim(t *testing.T) {
	prev := Run{Output: []byte("FAIL a.test.ts\n"), Exit: 1}
	cur := Run{Output: []byte("FAIL b.test.ts\n"), Exit: 1}
	claim := &FileClaim{Tool: "vitest", Failing: []string{"b.test.ts"}, Fixed: []string{"a.test.ts"}, New: []string{"b.test.ts"}}

	r := Diff(&prev, cur, Meta{Key: "k", FileClaim: claim}, Options{})
	if !r.Degraded || r.DegradeReason == nil || *r.DegradeReason != reasonSmall {
		t.Fatalf("want the small_output degrade (the common case), got degraded=%v reason=%v", r.Degraded, r.DegradeReason)
	}
	_, body := Render(r, Options{})
	for _, want := range []string{"failing (vitest): b.test.ts", "fixed (vitest): a.test.ts", "new (vitest): b.test.ts"} {
		if !strings.Contains(body, want) {
			t.Errorf("degraded body missing %q\ngot:\n%s", want, body)
		}
	}
}

// An interrupted run shows the partial capture, compares nothing, and claims
// nothing — a prefix of a run is not a run.
func TestDiff_interruptedComparesNothing(t *testing.T) {
	prev := Run{Output: []byte("PASS a\nPASS b\n"), Exit: 0}
	cur := Run{Output: []byte("PASS a\n"), Exit: -1, Interrupted: true}
	claim := &FileClaim{Tool: "go-test", Failing: []string{"pkg"}, Fixed: []string{"other"}, New: []string{}}

	r := Diff(&prev, cur, Meta{Key: "k", FileClaim: claim}, Options{})
	if r.Transition != string(TransitionInterrupted) || !r.Degraded {
		t.Fatalf("transition=%q degraded=%v, want interrupted+degraded", r.Transition, r.Degraded)
	}
	if r.Added != nil || r.Removed != nil || r.Unchanged != nil {
		t.Errorf("counts must stay null: a prefix is not a run (added=%v removed=%v)", r.Added, r.Removed)
	}
	if r.Tool != nil || r.Failing != nil || r.Fixed != nil || r.New != nil {
		t.Errorf("a truncated run must carry no claim, got tool=%v failing=%v fixed=%v", r.Tool, r.Failing, r.Fixed)
	}
	_, body := Render(r, Options{})
	if !strings.Contains(body, "PASS a") {
		t.Errorf("body must show the partial capture, got:\n%s", body)
	}
}

// The delta body must name itself. An agent whose command was wrapped by the
// hook did not type `rundiff`; if it reads a handful of lines where it expected
// a test log, the cheapest resolution it has is to run the command again
// unwrapped — and then the tool has cost more than it saved (the failure mode
// rtk#582 measured at +18% tokens). The legend is what makes that unnecessary.
func TestDelta_legendNamesTheOmission(t *testing.T) {
	prev := bigRun(1, "keep", "gone")
	cur := bigRun(1, "keep", "fresh")

	_, body := Render(Diff(&prev, cur, Meta{Key: "k"}, Options{}), Options{})
	if !strings.Contains(body, "delta only") || !strings.Contains(body, "--full") {
		t.Errorf("a non-empty delta must say what is omitted and how to get it back:\n%s", body)
	}

	// The empty delta is the most confusing output rundiff can produce (the
	// command ran; there is nothing to show) and the most common one in a
	// fix→test loop, so it gets the explicit form.
	_, body = Render(Diff(&prev, prev, Meta{Key: "k"}, Options{}), Options{})
	if !strings.Contains(body, "nothing changed") || !strings.Contains(body, "identical") {
		t.Errorf("an empty delta must say so in words:\n%s", body)
	}

	// …and none of it leaks into the machine channel, where the counts already
	// say it and prose would just be tokens.
	line, jsonBody := Render(Diff(&prev, cur, Meta{Key: "k"}, Options{JSON: true}), Options{JSON: true})
	if jsonBody != "" || strings.Contains(string(line), "delta only") {
		t.Errorf("prose leaked into --json:\nline: %s\nbody: %q", line, jsonBody)
	}
}

// The legend must not promise what --full does not do. --full RE-RUNS the
// command (runWrap always execs; there is no cache-reprint mode), so any wording
// that says "without re-running" or "from the cache" is a lie an agent will act
// on. This pins the honest phrasing.
func TestDelta_legendDoesNotClaimCacheReprint(t *testing.T) {
	prev := bigRun(1, "keep", "gone")
	for _, cur := range []Run{bigRun(1, "keep", "fresh"), prev} { // non-empty and empty delta
		_, body := Render(Diff(&prev, cur, Meta{Key: "k"}, Options{}), Options{})
		for _, lie := range []string{"without re-running", "from the cache", "from this same cache", "from the same cache"} {
			if strings.Contains(body, lie) {
				t.Errorf("legend claims %q, but --full re-runs the command:\n%s", lie, body)
			}
		}
		if !strings.Contains(body, "re-run") {
			t.Errorf("legend must state that --full re-runs:\n%s", body)
		}
	}
}
