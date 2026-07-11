package delta

import (
	"bytes"
	"unicode/utf8"
)

// Degrade reason enums (the JSON degrade_reason field). Degrading withholds the
// compact delta and prints bounded full output instead — it only ever shows
// MORE, never less, so the predicates are deliberately biased to fire.
const (
	reasonBinary     = "binary"
	reasonTooLarge   = "too_large"
	reasonInterleave = "interleave"
	reasonSmall      = "small_output"
	reasonHighChurn  = "high_churn"
)

const (
	maxInputBytes = 8 << 20 // 8 MiB per run
	maxInputLines = 50_000
	maxLineBytes  = 8192 // a physical line longer than this signals torn parallel output
	maxDeltaTotal = 2000 // added+removed above this is unpasteable → degrade
	minSmallLines = 3
	minSmallBytes = 2048
)

// splitLines splits raw output into lines on '\n', dropping the single empty
// element a trailing '\n' would otherwise produce (so "a\n" is one line, not
// two). Every remaining line — including a genuine interior blank — is kept.
func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := string(b)
	if s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	if s == "" {
		return []string{""} // the input was exactly "\n": one blank line
	}
	return splitOnNewline(s)
}

func splitOnNewline(s string) []string {
	out := make([]string, 0, bytes.Count([]byte(s), []byte{'\n'})+1)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// degradePreDiff runs the checks that must precede the multiset build: they null
// the counts and, for the size guard, avoid building the map at all.
func degradePreDiff(prev, cur []byte) (string, bool) {
	if binaryish(prev) || binaryish(cur) {
		return reasonBinary, true // G1
	}
	if len(prev) > maxInputBytes || len(cur) > maxInputBytes ||
		countLines(prev) > maxInputLines || countLines(cur) > maxInputLines {
		return reasonTooLarge, true // G2
	}
	if maxPhysicalLine(prev) > maxLineBytes || maxPhysicalLine(cur) > maxLineBytes {
		return reasonInterleave, true // G3
	}
	return "", false
}

// degradePostDiff runs the checks that need the real multiset counts.
func degradePostDiff(prev, cur lineSet, prevBytes, curBytes int, dc diffCounts, opt Options) (string, bool) {
	if min(prev.total, cur.total) < minSmallLines || max(prevBytes, curBytes) <= minSmallBytes {
		return reasonSmall, true // G4
	}
	if churn(dc.added, dc.removed, dc.unchanged) >= opt.ChurnLimit {
		return reasonHighChurn, true // G5
	}
	if dc.added+dc.removed > maxDeltaTotal {
		return reasonTooLarge, true // G6
	}
	return "", false
}

// binaryish reports whether output cannot be treated as line-oriented text: a
// NUL byte is definitive; otherwise a non-text byte fraction over 10% (control
// bytes other than tab/nl/cr/ESC, plus invalid UTF-8) over a 64 KiB head sample.
// ESC (0x1b) is excluded because dense ANSI-colored output (grep/ls --color,
// colored test runners) is exactly the input rundiff exists to diff — its ESC
// bytes must not push it over the threshold and defeat the normalized delta.
func binaryish(b []byte) bool {
	sample := b
	if len(sample) > 64<<10 {
		sample = sample[:64<<10]
	}
	if len(sample) == 0 {
		return false
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	nonText := 0
	for i := 0; i < len(sample); {
		r, size := utf8.DecodeRune(sample[i:])
		if r == utf8.RuneError && size == 1 {
			nonText++
			i++
			continue
		}
		if size == 1 {
			c := sample[i]
			if c < 0x20 && c != '\t' && c != '\n' && c != '\r' && c != 0x1b {
				nonText++
			}
		}
		i += size
	}
	return float64(nonText)/float64(len(sample)) > 0.10
}

func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := bytes.Count(b, []byte{'\n'})
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}

// maxPhysicalLine is the length in bytes of the longest run between newlines.
func maxPhysicalLine(b []byte) int {
	longest, start := 0, 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			if i-start > longest {
				longest = i - start
			}
			start = i + 1
		}
	}
	if len(b)-start > longest {
		longest = len(b) - start
	}
	return longest
}
