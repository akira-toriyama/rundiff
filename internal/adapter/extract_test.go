package adapter

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// argvFor returns an argv that hints the given tool (keeps invariant sweeps
// honest — detection is exercised, not bypassed).
func argvFor(tool string) []string {
	switch tool {
	case "go-test":
		return []string{"go", "test", "./..."}
	case "cargo-test":
		return []string{"cargo", "test"}
	default:
		return []string{tool}
	}
}

// TestExtract_invariants sweeps every committed capture pair (same tool) and
// asserts the claim contract from every angle at once.
func TestExtract_invariants(t *testing.T) {
	scenarios := captureScenarios(t)
	for _, prevSc := range scenarios {
		for _, curSc := range scenarios {
			if prevSc[0] != curSc[0] {
				continue // cross-tool pairs are covered by TestExtract_crossTool
			}
			tool := prevSc[0]
			prev := loadCapture(t, tool, prevSc[1])
			cur := loadCapture(t, tool, curSc[1])
			argv := argvFor(tool)
			got := Extract(argv, &prev, cur, "")
			if got == nil {
				continue // abstention is always legal; specific pairs are pinned per tool
			}

			name := tool + ":" + prevSc[1] + "→" + curSc[1]
			if got.Failing == nil {
				t.Errorf("%s: non-nil claim must carry non-nil Failing", name)
				continue
			}
			if (got.Fixed == nil) != (got.New == nil) {
				t.Errorf("%s: Fixed/New must be nil together: %v / %v", name, got.Fixed, got.New)
			}
			for _, s := range [][]string{got.Failing, got.Fixed, got.New} {
				if !slices.IsSorted(s) {
					t.Errorf("%s: unsorted slice %v", name, s)
				}
				if len(slices.Compact(slices.Clone(s))) != len(s) {
					t.Errorf("%s: duplicates in %v", name, s)
				}
			}
			for _, id := range got.New {
				if !slices.Contains(got.Failing, id) {
					t.Errorf("%s: New ⊄ Failing: %s", name, id)
				}
			}
			for _, id := range got.Fixed {
				if slices.Contains(got.Failing, id) {
					t.Errorf("%s: Fixed ∩ Failing ≠ ∅: %s", name, id)
				}
				if slices.Contains(got.New, id) {
					t.Errorf("%s: Fixed ∩ New ≠ ∅: %s", name, id)
				}
			}
			if cur.Exit == 0 && len(got.Failing) != 0 {
				t.Errorf("%s: exit 0 with failing=%v", name, got.Failing)
			}
			if prev.Exit == 0 && len(got.Fixed) != 0 {
				t.Errorf("%s: prev exit 0 with fixed=%v", name, got.Fixed)
			}
			// Verbatim-subset analog: every identity occurs as a substring of
			// the cleaned output of the run it is attributed to.
			curText := strings.Join(cleanLines(cur.Output), "\n")
			prevText := strings.Join(cleanLines(prev.Output), "\n")
			for _, id := range got.Failing {
				if !strings.Contains(curText, id) {
					t.Errorf("%s: failing %q not in current output", name, id)
				}
			}
			for _, id := range got.Fixed {
				if !strings.Contains(prevText, id) {
					t.Errorf("%s: fixed %q not in previous output", name, id)
				}
			}

			// Determinism: repeated call, byte-identical inputs ⇒ deep-equal.
			again := Extract(argv, &prev, cur, "")
			if !reflect.DeepEqual(got, again) {
				t.Errorf("%s: non-deterministic: %+v vs %+v", name, got, again)
			}

			// Symmetry where both directions claim a pair.
			if got.Fixed != nil {
				rev := Extract(argv, &cur, prev, "")
				if rev != nil && rev.Fixed != nil {
					if !reflect.DeepEqual(got.Fixed, rev.New) || !reflect.DeepEqual(got.New, rev.Fixed) {
						t.Errorf("%s: asymmetric: fixed=%v new=%v vs rev fixed=%v new=%v",
							name, got.Fixed, got.New, rev.Fixed, rev.New)
					}
				}
			}

			// Identity: a run against itself claims no changes.
			if prevSc[1] == curSc[1] {
				if got.Fixed != nil && (len(got.Fixed) != 0 || len(got.New) != 0) {
					t.Errorf("%s: self-pair claims fixed=%v new=%v", name, got.Fixed, got.New)
				}
			}

			// Purity: Extract must not have mutated the inputs.
			reload := loadCapture(t, tool, curSc[1])
			if !bytes.Equal(cur.Output, reload.Output) {
				t.Errorf("%s: Extract mutated its input", name)
			}
		}
	}
}

// TestExtract_summarySevered: truncate every failing capture at every line
// boundary; once the tool's sentinel is gone the per-run claim must be nil —
// and a surviving claim must never call a still-failing identity fixed.
func TestExtract_summarySevered(t *testing.T) {
	for _, sc := range captureScenarios(t) {
		tool, scenario := sc[0], sc[1]
		full := loadCapture(t, tool, scenario)
		if full.Exit == 0 {
			continue
		}
		argv := argvFor(tool)
		fullClaim := Extract(argv, nil, full, "")
		if fullClaim == nil {
			continue
		}
		lines := bytes.Split(full.Output, []byte("\n"))
		for cut := 0; cut < len(lines); cut++ {
			truncated := Run{Exit: full.Exit, Output: bytes.Join(lines[:cut], []byte("\n"))}
			got := Extract(argv, &full, truncated, "")
			if got == nil {
				continue // abstained: always safe
			}
			for _, id := range got.Fixed {
				if slices.Contains(fullClaim.Failing, id) {
					t.Fatalf("%s/%s cut@%d: truncation produced false fixed=%q", tool, scenario, cut, id)
				}
			}
		}
	}
}

// TestExtract_neverFalseFixed is the mechanized safety theorem (the adapter
// analog of TestNormalize_realChangeSurvivesNoise): for every same-tool capture
// pair where identity X fails in BOTH runs, delete each single line of the
// current output in turn — the result must be nil or must not claim X fixed.
func TestExtract_neverFalseFixed(t *testing.T) {
	scenarios := captureScenarios(t)
	for _, prevSc := range scenarios {
		for _, curSc := range scenarios {
			if prevSc[0] != curSc[0] {
				continue
			}
			tool := prevSc[0]
			prev := loadCapture(t, tool, prevSc[1])
			cur := loadCapture(t, tool, curSc[1])
			argv := argvFor(tool)
			base := Extract(argv, &prev, cur, "")
			if base == nil || len(base.Failing) == 0 {
				continue
			}
			prevClaim := Extract(argv, nil, prev, "")
			if prevClaim == nil {
				continue
			}
			// Identities failing in both runs — the ones a mutation must never
			// flip to "fixed".
			var stuck []string
			for _, id := range base.Failing {
				if slices.Contains(prevClaim.Failing, id) {
					stuck = append(stuck, id)
				}
			}
			if len(stuck) == 0 {
				continue
			}
			lines := bytes.Split(cur.Output, []byte("\n"))
			for drop := 0; drop < len(lines); drop++ {
				mutated := make([][]byte, 0, len(lines)-1)
				mutated = append(mutated, lines[:drop]...)
				mutated = append(mutated, lines[drop+1:]...)
				got := Extract(argv, &prev, Run{Exit: cur.Exit, Output: bytes.Join(mutated, []byte("\n"))}, "")
				if got == nil {
					continue
				}
				for _, id := range stuck {
					if slices.Contains(got.Fixed, id) {
						t.Fatalf("%s: dropping line %d of %s claims still-failing %q fixed",
							tool, drop, curSc[1], id)
					}
				}
			}
		}
	}
}

func TestExtract_nullTable(t *testing.T) {
	goFail := loadCapture(t, "go-test", "fail")
	big := bytes.Repeat([]byte("x\n"), maxInputLines+1)

	cases := []struct {
		name string
		argv []string
		prev *Run
		cur  Run
		tool string
	}{
		{"forced none", []string{"go", "test"}, nil, goFail, "none"},
		{"unknown forced tool", []string{"go", "test"}, nil, goFail, "no-such-tool"},
		{"NUL byte", []string{"go", "test"}, nil, Run{Exit: 1, Output: []byte("FAIL\tp\t0.1s\n\x00")}, ""},
		{"too many lines", []string{"go", "test"}, nil, Run{Exit: 1, Output: big}, ""},
		{"signal exit", []string{"go", "test"}, nil, Run{Exit: -1, Output: goFail.Output}, ""},
		{"clean-empty without silent-clean tool", []string{"go", "test"}, &goFail, Run{Exit: 0, Output: nil}, ""},
		{"both runs clean-empty", []string{"tsc"}, &Run{Exit: 0, Output: nil}, Run{Exit: 0, Output: []byte("  \n")}, ""},
		{"exit 0 but failures parsed", []string{"go", "test"}, nil, Run{Exit: 0, Output: goFail.Output}, ""},
		{"failing exit, nothing identified", []string{"go", "test"}, nil, Run{Exit: 1, Output: []byte("panic: boom\n")}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Extract(c.argv, c.prev, c.cur, c.tool); got != nil {
				t.Errorf("claim=%+v want nil", got)
			}
		})
	}

	// An argv that hints a DIFFERENT registered tool narrows the candidate set
	// away from the one whose shape the output has ⇒ nil (argv can veto, never
	// certify). Needs a second registered parser to express.
	t.Run("argv hints another tool", func(t *testing.T) {
		for _, name := range Tools() {
			if name == "go-test" {
				continue
			}
			if got := Extract(argvFor(name), nil, goFail, ""); got != nil {
				t.Errorf("argv for %s over go-test output: claim=%+v want nil", name, got)
			}
			return
		}
		t.Skip("only go-test registered so far")
	})
}

// Over-cap failing sets nil the whole claim — a truth claim is never clipped.
func TestExtract_identityCap(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxIdentities+1; i++ {
		b.WriteString("FAIL\texample.com/pkg")
		for range i {
			b.WriteByte('x')
		}
		b.WriteString("\t0.1s\n")
	}
	if got := Extract([]string{"go", "test"}, nil, Run{Exit: 1, Output: []byte(b.String())}, ""); got != nil {
		t.Errorf("claim with %d identities want nil", len(got.Failing))
	}
}

// A forced tool selects a parser but never bypasses the fingerprint gate.
func TestExtract_forcedStillGated(t *testing.T) {
	pytestish := Run{Exit: 1, Output: []byte("=== test session starts ===\nFAILED tests/x.py::t\n=== 1 failed in 0.1s ===\n")}
	if got := Extract([]string{"pytest"}, nil, pytestish, "go-test"); got != nil {
		t.Errorf("forcing go-test onto pytest output produced %+v, want nil", got)
	}
}
