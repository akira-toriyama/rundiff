package adapter

import (
	"slices"
	"strings"
	"testing"
)

// FuzzExtract hunts gate bypasses, not just crashes: on arbitrary byte pairs
// Extract must never panic, and every non-nil claim must satisfy the full
// contract (sorted, deduped, set algebra, exit consistency, verbatim-subset).
func FuzzExtract(f *testing.F) {
	f.Add([]byte("ok  \tpkg\t0.1s\n"), 0, []byte("FAIL\tpkg\t0.1s\nFAIL\n"), 1, byte(0))
	f.Add([]byte("--- FAIL: TestX (0.00s)\nFAIL\tp\t0.2s\n"), 1, []byte("ok  \tp\t0.2s\n"), 0, byte(1))
	f.Add([]byte(""), 0, []byte("\x1b[31mFAIL\tp\t1s\x1b[0m\n"), 1, byte(2))
	f.Add([]byte("?   \tp\t[no test files]\n"), 0, []byte("\x00"), -1, byte(3))
	// Every committed capture, paired with its own tool's other scenarios —
	// the fuzzer mutates from real transcripts, where the gates matter most.
	seeds := map[string][]Run{}
	for _, sc := range captureScenarios(f) {
		seeds[sc[0]] = append(seeds[sc[0]], loadCapture(f, sc[0], sc[1]))
	}
	idx := byte(0)
	for _, runs := range seeds {
		for _, prev := range runs {
			for _, cur := range runs {
				f.Add(prev.Output, prev.Exit, cur.Output, cur.Exit, idx)
				idx++
			}
		}
	}
	f.Fuzz(func(t *testing.T, prevOut []byte, prevExit int, curOut []byte, curExit int, toolIdx byte) {
		tools := append(Tools(), "", "none")
		forced := tools[int(toolIdx)%len(tools)]
		prev := &Run{Output: prevOut, Exit: prevExit}
		cur := Run{Output: curOut, Exit: curExit}

		for _, p := range []*Run{prev, nil} {
			got := Extract([]string{"go", "test"}, p, cur, forced)
			if got == nil {
				continue
			}
			if got.Failing == nil {
				t.Fatal("non-nil claim with nil Failing")
			}
			if (got.Fixed == nil) != (got.New == nil) {
				t.Fatalf("Fixed/New not nil-paired: %v / %v", got.Fixed, got.New)
			}
			if p == nil && got.Fixed != nil {
				t.Fatalf("baseline with a cross-run claim: %v", got.Fixed)
			}
			for _, s := range [][]string{got.Failing, got.Fixed, got.New} {
				if !slices.IsSorted(s) || len(slices.Compact(slices.Clone(s))) != len(s) {
					t.Fatalf("unsorted or duplicated: %v", s)
				}
			}
			for _, id := range got.New {
				if !slices.Contains(got.Failing, id) {
					t.Fatalf("New ⊄ Failing: %q", id)
				}
			}
			for _, id := range got.Fixed {
				if slices.Contains(got.Failing, id) {
					t.Fatalf("Fixed ∩ Failing: %q", id)
				}
			}
			if cur.Exit == 0 && len(got.Failing) != 0 {
				t.Fatalf("exit 0 with failing=%v", got.Failing)
			}
			curText := strings.Join(cleanLines(cur.Output), "\n")
			for _, id := range got.Failing {
				if !strings.Contains(curText, id) {
					t.Fatalf("failing %q not a substring of the current output", id)
				}
			}
		}
	})
}

// FuzzCleanLine: the parse-side chrome stripper is total, idempotent and
// single-line on arbitrary bytes.
func FuzzCleanLine(f *testing.F) {
	for _, s := range []string{"", "plain", "\x1b[31mred\x1b[0m", "\r\rframe", "a\rb", "\x1b]0;title\x07x", "\x1b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		s = strings.ReplaceAll(s, "\n", "") // cleanLine operates on split lines
		once := cleanLine(s)
		if strings.ContainsAny(once, "\n") {
			t.Fatalf("cleanLine introduced a newline: %q → %q", s, once)
		}
		if twice := cleanLine(once); twice != once {
			t.Fatalf("cleanLine not idempotent: %q → %q → %q", s, once, twice)
		}
	})
}
