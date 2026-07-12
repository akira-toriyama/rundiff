package delta

import (
	"strings"
	"testing"
)

// FuzzNormalize asserts the per-line invariants on arbitrary bytes: never
// panics, is idempotent, and never introduces a newline.
func FuzzNormalize(f *testing.F) {
	seeds := []string{
		"", "plain", "\x1b[31mred\x1b[0m", "2026-07-11T00:00:00Z", "goroutine 9",
		"\r\r\rx", "/tmp/a/b", "12:00:00.5 took 3m2s", "\x00\x01\x02", "µs 5µs",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n := newNormalizer(Options{})
		// line() is only ever called on already-split lines, so exercise the
		// real pipeline: split, then normalize each line.
		for _, ln := range splitLines([]byte(s)) {
			once := n.line(ln)
			if strings.Contains(once, "\n") {
				t.Fatalf("normalize introduced a newline: %q → %q", ln, once)
			}
			if twice := n.line(once); twice != once {
				t.Fatalf("normalize not idempotent: %q → %q → %q", ln, once, twice)
			}
		}
	})
}

// FuzzDiff asserts the diff invariants on arbitrary output pairs: never panics,
// counts are non-negative, and conservation holds whenever counts are present.
func FuzzDiff(f *testing.F) {
	f.Add([]byte("a\nb\nc\n"), 0, []byte("a\nX\nc\n"), 1)
	f.Add([]byte(""), 0, []byte(""), 0)
	f.Add([]byte("dup\ndup\n"), 1, []byte("dup\n"), 0)
	f.Add([]byte("\x1b[31m2026-01-01T00:00:00Z\x1b[0m\n"), 0, []byte("\x00\n"), 1)
	f.Fuzz(func(t *testing.T, prevOut []byte, prevExit int, curOut []byte, curExit int) {
		prev := &Run{Exit: prevExit, Output: prevOut}
		cur := Run{Exit: curExit, Output: curOut}
		r := Diff(prev, cur, Meta{Key: "k"}, Options{})

		// Render must never panic either, in both modes.
		_, _ = Render(r, Options{})
		_, _ = Render(r, Options{JSON: true})

		if r.Added == nil {
			return // counts nulled (baseline/binary/too_large/interleave)
		}
		if *r.Added < 0 || *r.Removed < 0 || *r.Unchanged < 0 {
			t.Fatalf("negative count: +%d -%d ~%d", *r.Added, *r.Removed, *r.Unchanged)
		}
		if *r.Unchanged+*r.Removed != *r.TotalPrev {
			t.Fatalf("conservation prev: ~%d + -%d != total_prev %d", *r.Unchanged, *r.Removed, *r.TotalPrev)
		}
		if *r.Unchanged+*r.Added != *r.TotalCur {
			t.Fatalf("conservation cur: ~%d + +%d != total_cur %d", *r.Unchanged, *r.Added, *r.TotalCur)
		}
	})
}
