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
