package adapter

import (
	"reflect"
	"testing"
)

var goArgv = []string{"go", "test", "./..."}

func TestGoTest_capturePairs(t *testing.T) {
	pass := loadCapture(t, "go-test", "pass")
	fail := loadCapture(t, "go-test", "fail")
	builderr := loadCapture(t, "go-test", "builderr")

	cases := []struct {
		name        string
		prev        *Run
		cur         Run
		wantFailing []string
		wantFixed   []string
		wantNew     []string
		wantNilPair bool // Fixed/New nil (no cross-run claim)
	}{
		{
			name: "baseline fail", prev: nil, cur: fail,
			wantFailing: []string{"example.com/fixture/calc"}, wantNilPair: true,
		},
		{
			name: "fail then fixed", prev: &fail, cur: pass,
			wantFailing: []string{}, wantFixed: []string{"example.com/fixture/calc"}, wantNew: []string{},
		},
		{
			name: "pass then regressed", prev: &pass, cur: fail,
			wantFailing: []string{"example.com/fixture/calc"},
			wantFixed:   []string{}, wantNew: []string{"example.com/fixture/calc"},
		},
		{
			name: "still failing, same package", prev: &fail, cur: fail,
			wantFailing: []string{"example.com/fixture/calc"},
			wantFixed:   []string{}, wantNew: []string{},
		},
		{
			name: "test failure then build error is still failing", prev: &fail, cur: builderr,
			wantFailing: []string{"example.com/fixture/calc"},
			wantFixed:   []string{}, wantNew: []string{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Extract(goArgv, nil, c.prev, c.cur, "")
			if got == nil {
				t.Fatal("Extract returned nil, want a claim")
			}
			if got.Tool != "go-test" {
				t.Fatalf("tool=%s want go-test", got.Tool)
			}
			if !reflect.DeepEqual(got.Failing, c.wantFailing) {
				t.Errorf("failing=%v want %v", got.Failing, c.wantFailing)
			}
			if c.wantNilPair {
				if got.Fixed != nil || got.New != nil {
					t.Errorf("fixed=%v new=%v want nil pair", got.Fixed, got.New)
				}
				return
			}
			if !reflect.DeepEqual(got.Fixed, c.wantFixed) || !reflect.DeepEqual(got.New, c.wantNew) {
				t.Errorf("fixed=%v new=%v want %v/%v", got.Fixed, got.New, c.wantFixed, c.wantNew)
			}
		})
	}
}

// Verbose (-v) output carries === RUN / --- FAIL noise; identities must still
// come from the trailers alone.
func TestGoTest_verbose(t *testing.T) {
	vfail := loadCapture(t, "go-test", "verbose-fail")
	vpass := loadCapture(t, "go-test", "verbose-pass")
	got := Extract([]string{"go", "test", "-v", "./calc/"}, nil, &vfail, vpass, "")
	if got == nil {
		t.Fatal("nil claim")
	}
	want := []string{"example.com/fixture/calc"}
	if !reflect.DeepEqual(got.Fixed, want) || len(got.New) != 0 || len(got.Failing) != 0 {
		t.Errorf("fixed=%v new=%v failing=%v want fixed=%v", got.Fixed, got.New, got.Failing, want)
	}
}

func TestGoTest_parseGates(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		exit  int
	}{
		{"exit 2 is go's own error", []string{"FAIL\texample.com/x\t0.1s"}, 2},
		{"failing exit but no FAIL trailer", []string{"--- FAIL: TestX (0.00s)", "some panic text"}, 1},
		{"clean exit but a FAIL trailer", []string{"FAIL\texample.com/x\t0.1s"}, 0},
		{"FAIL marks with trailers lost (torn)", []string{"--- FAIL: TestX (0.00s)", "ok  \texample.com/y\t0.1s"}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := (goTest{}).parse(c.lines, c.exit); ok {
				t.Error("parse ok, want refused")
			}
		})
	}
}

// A deleted package (its trailer becomes `? pkg [no test files]`) is not pass
// evidence: prev-failing pkg unaccounted ⇒ the cross-run pair is withheld.
func TestGoTest_noTestFilesIsNotPassEvidence(t *testing.T) {
	prev := Run{Exit: 1, Output: []byte("FAIL\texample.com/fixture/calc\t0.2s\nFAIL\n")}
	cur := Run{Exit: 0, Output: []byte("?   \texample.com/fixture/calc\t[no test files]\nok  \texample.com/fixture/str\t0.1s\n")}
	got := Extract(goArgv, nil, &prev, cur, "")
	if got == nil {
		t.Fatal("nil claim, want failing-only claim")
	}
	if len(got.Failing) != 0 {
		t.Errorf("failing=%v want []", got.Failing)
	}
	if got.Fixed != nil || got.New != nil {
		t.Errorf("fixed=%v new=%v want nil pair (deletion is not a fix)", got.Fixed, got.New)
	}
}

// The global clean-run proof needs the tool's own zero-failure output (an ok
// trailer), not a bare exit 0: --- PASS marks alone must not prove a fix for a
// package with no trailer of its own.
func TestGoTest_cleanRunNeedsOKTrailer(t *testing.T) {
	prev := Run{Exit: 1, Output: []byte("FAIL\texample.com/fixture/calc\t0.2s\nFAIL\n")}
	cur := Run{Exit: 0, Output: []byte("--- PASS: TestOther (0.00s)\nPASS\n")}
	got := Extract(goArgv, nil, &prev, cur, "")
	if got == nil {
		t.Fatal("nil claim")
	}
	if got.Fixed != nil {
		t.Errorf("fixed=%v want nil (no ok trailer ⇒ no global pass proof)", got.Fixed)
	}
}

func TestGoTest_blockedFlags(t *testing.T) {
	fail := loadCapture(t, "go-test", "fail")
	pass := loadCapture(t, "go-test", "pass")
	for _, argv := range [][]string{
		{"go", "test", "-json", "./..."},
		{"go", "test", "-fuzz", "FuzzX", "./..."},
	} {
		if got := Extract(argv, nil, &fail, pass, ""); got != nil {
			t.Errorf("argv=%v: claim=%+v want nil (blocked flag)", argv, got)
		}
	}
}
