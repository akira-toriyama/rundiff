package adapter

import (
	"regexp"
	"strings"
)

// Line cleaning for PARSING only — the cleaned text is matched against tool
// anchors and never displayed, so stripping here can never hide a change from
// the user. The rules mirror internal/delta's Stage 0/1 (CR handling + ANSI)
// but are deliberately duplicated: delta and adapter are both import-free
// leaves, and ~30 stable lines are cheaper than a dependency edge between the
// two safety arguments.
var (
	reCSI    = regexp.MustCompile("\x1b\\[[0-9:;<=>?]*[ -/]*[@-~]")
	reOSC    = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)?")
	reStrEsc = regexp.MustCompile("\x1b[PX^_][^\x1b]*(?:\x1b\\\\|\x07)?")
	reNF     = regexp.MustCompile("\x1b[ -/]*[0-~]")
)

// cleanLine trims one trailing \r (CRLF), collapses interior \r progress frames
// to the last segment, and removes ANSI/ESC sequences. Idempotent, total,
// single-line.
func cleanLine(s string) string {
	s = strings.TrimSuffix(s, "\r")
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		s = s[i+1:]
	}
	if strings.IndexByte(s, '\x1b') < 0 {
		return s
	}
	s = reCSI.ReplaceAllString(s, "")
	s = reOSC.ReplaceAllString(s, "")
	s = reStrEsc.ReplaceAllString(s, "")
	s = reNF.ReplaceAllString(s, "")
	if strings.IndexByte(s, '\x1b') >= 0 {
		s = strings.ReplaceAll(s, "\x1b", "") // lone ESC, after the composed forms
	}
	return s
}

// cleanLines splits raw output into cleaned lines. Like delta's splitLines, a
// single trailing '\n' does not produce a final empty element.
func cleanLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := string(b)
	if s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	if s == "" {
		return []string{""}
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = cleanLine(l)
	}
	return lines
}

func allBlank(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return false
		}
	}
	return true
}
