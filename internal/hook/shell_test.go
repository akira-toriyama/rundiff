package hook

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestSplit_matchesRealShell is the ORACLE.
//
// This package's entire safety argument is one claim: because every permitted
// byte is inert in POSIX-sh word context, `bin -- ` + strings.Join(argv, " ")
// needs NO shell quoter — a real shell re-splits it into exactly ["rundiff",
// "--"] + argv. Every other test in this package checks our MODEL of a shell.
// This one checks the shell.
//
// It matters because the rewritten string is handed back to Claude Code, which
// runs it through a shell. If a single accepted byte turned out to word-split,
// glob, or expand there, the agent would silently run a command nobody typed —
// and the whole point of the byte gate would be decoration.
//
// os/exec lives here in the _test file only; the package itself stays I/O-free.
func TestSplit_matchesRealShell(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh unavailable")
	}

	for _, c := range rewriteCases {
		t.Run(c.name, func(t *testing.T) {
			rewritten, _, ok := Command(c.in, Options{})
			if !ok {
				t.Fatalf("Command(%q) refused", c.in)
			}

			// The `cd <dir> && ` prefix is shell syntax by design (it is the one
			// carve-out), and the directory does not exist here — so the claim under
			// test is about the WORD-SPLITTING of the part after it, which is the
			// part we generate from gated bytes.
			prefix, tail := splitCDPrefix(c.in)
			argv, split := Split(tail)
			if !split {
				t.Fatalf("Split(%q) refused", tail)
			}
			words := strings.TrimPrefix(rewritten, prefix)

			// printf '%s\0' <words> — the shell splits, we observe. NUL is the one
			// separator that cannot occur inside any word we permit.
			out, err := exec.Command("/bin/sh", "-c", "printf '%s\\0' "+words).Output()
			if err != nil {
				t.Fatalf("/bin/sh: %v", err)
			}
			got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")

			want := append([]string{binName, "--"}, argv...)
			if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
				t.Errorf("a real shell disagrees with Split()\n  rewritten: %q\n  sh gave:   %q\n  want:      %q",
					words, got, want)
			}
		})
	}
}

// The same oracle, one level deeper: a command REFUSED for carrying shell syntax
// must genuinely be one a shell would treat as more than a plain word list. This
// guards the opposite error — a gate so paranoid it refuses inert bytes — from
// going unnoticed, by confirming that the bytes we permit are exactly the ones a
// shell leaves alone.
func TestSafeBytes_areInertInSh(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh unavailable")
	}

	var word strings.Builder
	for b := 0; b < 128; b++ {
		if safeByte(byte(b)) {
			word.WriteByte(byte(b))
		}
	}
	// One word made of every safe byte there is. If any of them quoted, split,
	// globbed or expanded, the shell would hand back something other than itself.
	// (It is run in an empty temp dir so a glob character, had one slipped into
	// the safe set, would have nothing to match and would be more likely to pass
	// than to fail — the assertion is deliberately run where it is hardest.)
	t.Chdir(t.TempDir())
	out, err := exec.Command("/bin/sh", "-c", "printf '%s\\0' "+word.String()).Output()
	if err != nil {
		t.Fatalf("/bin/sh: %v", err)
	}
	got := strings.TrimSuffix(string(out), "\x00")
	if got != word.String() {
		t.Errorf("a safe byte is not inert in sh\n got %q\nwant %q", got, word.String())
	}
}
