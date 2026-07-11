package delta

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestDegrade_binaryNullsCounts(t *testing.T) {
	prev := Run{Exit: 0, Output: []byte("normal text\nsecond line\nthird\n")}
	cur := Run{Exit: 0, Output: append([]byte("has a nul\x00 byte\n"), bytes.Repeat([]byte("x\n"), 5)...)}
	r := Diff(&prev, cur, 0, "k", Options{})
	if !r.Degraded || r.DegradeReason == nil || *r.DegradeReason != reasonBinary {
		t.Fatalf("degraded=%v reason=%v want binary", r.Degraded, r.DegradeReason)
	}
	if r.Added != nil || r.Removed != nil || r.Unchanged != nil {
		t.Error("binary degrade must null the counts")
	}
	// The transition is still trustworthy.
	if r.Transition != string(TransitionStillPassing) {
		t.Errorf("transition=%s want still_passing", r.Transition)
	}
}

func TestDegrade_tooLargeBytesNullsCounts(t *testing.T) {
	big := bytes.Repeat([]byte("line of text here\n"), 600_000) // > 8 MiB and > 50k lines
	prev := Run{Exit: 0, Output: []byte("small\nbaseline\nhere\n")}
	cur := Run{Exit: 0, Output: big}
	r := Diff(&prev, cur, 0, "k", Options{})
	if !r.Degraded || *r.DegradeReason != reasonTooLarge {
		t.Fatalf("reason=%v want too_large", r.DegradeReason)
	}
	if r.Added != nil {
		t.Error("too_large(input) must null the counts")
	}
}

func TestDegrade_interleaveLongLine(t *testing.T) {
	prev := Run{Exit: 0, Output: []byte("a\nb\nc\nd\n")}
	cur := Run{Exit: 0, Output: append(append([]byte("a\n"), bytes.Repeat([]byte("x"), maxLineBytes+1)...), []byte("\nb\n")...)}
	r := Diff(&prev, cur, 0, "k", Options{})
	if !r.Degraded || *r.DegradeReason != reasonInterleave {
		t.Fatalf("reason=%v want interleave", r.DegradeReason)
	}
	if r.Added != nil {
		t.Error("interleave must null the counts")
	}
}

func TestDegrade_smallOutputKeepsRealCounts(t *testing.T) {
	prev := run(1, "a", "b", "c")
	cur := run(1, "a", "X", "c")
	r := Diff(&prev, cur, 0, "k", Options{})
	if !r.Degraded || *r.DegradeReason != reasonSmall {
		t.Fatalf("reason=%v want small_output", r.DegradeReason)
	}
	// G4 keeps real counts.
	if r.Added == nil || *r.Added != 1 || *r.Removed != 1 {
		t.Errorf("small_output should keep real counts, got added=%v removed=%v", r.Added, r.Removed)
	}
}

func TestDegrade_highChurn(t *testing.T) {
	// Two large outputs sharing almost nothing → churn ≥ 0.5.
	var prevB, curB strings.Builder
	for i := 0; i < 200; i++ {
		prevB.WriteString("old unique line ")
		prevB.WriteString(strings.Repeat("p", 1))
		prevB.WriteString(itoa(i))
		prevB.WriteByte('\n')
		curB.WriteString("new unique line ")
		curB.WriteString(itoa(i))
		curB.WriteByte('\n')
	}
	prev := Run{Exit: 1, Output: []byte(prevB.String())}
	cur := Run{Exit: 1, Output: []byte(curB.String())}
	r := Diff(&prev, cur, 0, "k", Options{})
	if !r.Degraded || *r.DegradeReason != reasonHighChurn {
		t.Fatalf("reason=%v want high_churn (churn=%v)", r.DegradeReason, deref(r.Churn))
	}
	if r.Added == nil {
		t.Error("high_churn should keep real counts")
	}
}

func TestDegrade_tooLargeByDeltaCountKeepsCounts(t *testing.T) {
	// G6: added+removed > maxDeltaTotal (2000) while churn stays below the limit
	// (so G5 does not preempt) → degrade too_large, but the counts stay REAL —
	// unlike the input-size G2, which nulls them. This is the "unpasteable delta"
	// guard, and the only too_large path that keeps counts.
	var prevB, curB strings.Builder
	for i := 0; i < 3000; i++ { // shared lines keep churn well below 0.5
		fmt.Fprintf(&prevB, "shared %05d\n", i)
		fmt.Fprintf(&curB, "shared %05d\n", i)
	}
	for i := 0; i < 1100; i++ { // 1100 removed + 1100 added = 2200 > 2000
		fmt.Fprintf(&prevB, "prev-only %05d\n", i)
		fmt.Fprintf(&curB, "cur-only %05d\n", i)
	}
	prev := Run{Exit: 1, Output: []byte(prevB.String())}
	cur := Run{Exit: 1, Output: []byte(curB.String())}
	r := Diff(&prev, cur, 0, "k", Options{})
	if !r.Degraded || r.DegradeReason == nil || *r.DegradeReason != reasonTooLarge {
		t.Fatalf("reason=%v want too_large (G6)", r.DegradeReason)
	}
	if r.Added == nil {
		t.Fatal("G6 too_large-by-delta-count should KEEP real counts (unlike the G2 input-size guard)")
	}
	if *r.Added != 1100 || *r.Removed != 1100 {
		t.Errorf("counts: added=%d removed=%d want 1100/1100", *r.Added, *r.Removed)
	}
}

func TestDegrade_notTriggeredForModestDelta(t *testing.T) {
	prev := bigRun(0, "a", "b", "c", "d")
	cur := bigRun(0, "a", "b", "c", "d", "e") // one added line among lots of filler
	r := Diff(&prev, cur, 0, "k", Options{})
	if r.Degraded {
		t.Errorf("a small trusted delta should not degrade (reason=%v)", r.DegradeReason)
	}
	if *r.Added != 1 {
		t.Errorf("added=%d want 1", *r.Added)
	}
}

func TestDegrade_ansiColorNotBinary(t *testing.T) {
	// Dense ANSI-colored output (grep/ls --color, colored test runners) must NOT
	// be misclassified as binary — that would suppress the normalized delta the
	// tool exists to produce. Build ~30 short heavily-colored lines.
	var prevB, curB strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&prevB, "\x1b[32mPASS\x1b[0m t%d \x1b[90m(0)\x1b[0m\n", i)
		fmt.Fprintf(&curB, "\x1b[32mPASS\x1b[0m t%d \x1b[90m(0)\x1b[0m\n", i)
	}
	// Change one line's status in cur.
	curStr := strings.Replace(curB.String(), "\x1b[32mPASS\x1b[0m t7", "\x1b[31mFAIL\x1b[0m t7", 1)
	prev := Run{Exit: 0, Output: []byte(prevB.String())}
	cur := Run{Exit: 1, Output: []byte(curStr)}
	r := Diff(&prev, cur, 0, "k", Options{})
	if r.Degraded && *r.DegradeReason == reasonBinary {
		t.Fatalf("colored output wrongly degraded as binary")
	}
}

func fptr(f float64) *float64 { return &f }

func TestDegrade_churnZeroHonored(t *testing.T) {
	// An explicit `--churn 0` (ChurnLimit pointing at 0) means "degrade on any
	// change": a modest delta that stays a trusted delta at the default 0.5 must
	// degrade for high_churn here. Guards the *float64 sentinel fix — a plain
	// float64 zero would have collided with the unset default and silently used 0.5.
	prev := bigRun(0, "a", "b", "c", "d")
	cur := bigRun(0, "a", "b", "c", "e") // one changed line among constant filler
	if def := Diff(&prev, cur, 0, "k", Options{}); def.Degraded && *def.DegradeReason == reasonHighChurn {
		t.Fatal("precondition: this delta should not high_churn-degrade at the default 0.5")
	}
	got := Diff(&prev, cur, 0, "k", Options{ChurnLimit: fptr(0)})
	if !got.Degraded || got.DegradeReason == nil || *got.DegradeReason != reasonHighChurn {
		t.Fatalf("--churn 0 should degrade on any change: degraded=%v reason=%v", got.Degraded, got.DegradeReason)
	}
	if got.Added == nil { // G5 keeps real counts
		t.Error("high_churn degrade should keep real counts")
	}
}

func TestDegrade_bothEmpty(t *testing.T) {
	prev := Run{Exit: 0, Output: nil}
	cur := Run{Exit: 0, Output: []byte{}}
	r := Diff(&prev, cur, 0, "k", Options{}) // must not panic; degrades small
	if !r.Degraded {
		t.Error("empty vs empty should degrade (small_output)")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func deref(f *float64) float64 {
	if f == nil {
		return -1
	}
	return *f
}
