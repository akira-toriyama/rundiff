package delta

import (
	"strings"
	"testing"
)

func norm(s string) string { return newNormalizer(Options{}).line(s) }

func TestNormalize_ansiStripped(t *testing.T) {
	cases := []string{
		"\x1b[31mFAIL\x1b[0m",                  // CSI color
		"\x1b[1;32mPASS\x1b[0m",                // multi-param CSI
		"\x1b]8;;http://x\x07FAIL\x1b]8;;\x07", // OSC hyperlink around FAIL
	}
	for _, in := range cases {
		got := norm(in)
		if strings.ContainsRune(got, '\x1b') {
			t.Errorf("norm(%q) still has ESC: %q", in, got)
		}
		if !strings.Contains(strings.ToUpper(got), "FAIL") && !strings.Contains(got, "PASS") {
			t.Errorf("norm(%q) lost the payload: %q", in, got)
		}
	}
}

func TestNormalize_timestampsAndDurations(t *testing.T) {
	cases := []struct{ a, b string }{
		{"2026-07-11T21:00:00Z build ok", "2026-07-11T09:59:59.123Z build ok"}, // ISO
		{"[12:00:01] starting", "[23:59:59] starting"},                         // clock
		{"ok  github.com/x  0.512s", "ok  github.com/x  1.204s"},               // dec-sec dur
		{"done in 250ms", "done in 999ms"},                                     // abbrev dur
		{"finished in 3s", "finished in 42s"},                                  // keyword int dur
		{"took 1m2.5s", "took 3h4m5s"},                                         // compound dur
		{"serving on [::1]:54321", "serving on [::1]:54999"},                   // IPv6 loopback port
	}
	for _, c := range cases {
		if na, nb := norm(c.a), norm(c.b); na != nb {
			t.Errorf("time/dur noise not cancelled:\n  %q → %q\n  %q → %q", c.a, na, c.b, nb)
		}
	}
}

func TestNormalize_tempPathsAndIdentifiers(t *testing.T) {
	cases := []struct{ a, b string }{
		{"wrote /var/folders/xy/abc123/T/pytest-of-me/pytest-1/f", "wrote /var/folders/zz/999/T/pytest-of-me/pytest-2/f"},
		{"tmp /tmp/build-aaaa/out", "tmp /tmp/build-bbbb/out"},
		{"goroutine 17 [running]:", "goroutine 8391 [running]:"},
		{"id 550e8400-e29b-41d4-a716-446655440000 done", "id 550e8400-e29b-41d4-a716-446655440099 done"},
		{"listening on 127.0.0.1:54321", "listening on 127.0.0.1:12345"},
	}
	for _, c := range cases {
		if na, nb := norm(c.a), norm(c.b); na != nb {
			t.Errorf("noise not cancelled:\n  %q → %q\n  %q → %q", c.a, na, c.b, nb)
		}
	}
}

func TestNormalize_idempotent(t *testing.T) {
	inputs := []string{
		"\x1b[31m2026-07-11T21:00:00Z\x1b[0m done in 5ms goroutine 3",
		"plain text line",
		"12:00:00 /tmp/x/y 0x1f",
		"",
		"\r\r\rprogress\rfinal frame",
	}
	for _, in := range inputs {
		once := norm(in)
		twice := norm(once)
		if once != twice {
			t.Errorf("not idempotent: norm(%q)=%q, norm(norm)=%q", in, once, twice)
		}
		if strings.Contains(once, "\n") {
			t.Errorf("norm(%q) introduced a newline: %q", in, once)
		}
	}
}

func TestNormalize_crCollapse(t *testing.T) {
	if got := norm("50%\r75%\r100% done"); got != "100% done" {
		t.Errorf("CR-collapse: got %q want %q", got, "100% done")
	}
	if got := norm("trailing\r"); got != "trailing" {
		t.Errorf("trailing CR: got %q want %q", got, "trailing")
	}
}

func TestNormalize_optInRulesDefaultOff(t *testing.T) {
	// A bare 0x… value, a git-sha-like hex, and a bare date are NOT masked by
	// default — masking them could hide a real change.
	for _, in := range []string{"error 0xdeadbeef", "commit a1b2c3d4e5f6", "released 2026-07-11"} {
		if strings.Contains(norm(in), "<") {
			t.Errorf("norm(%q)=%q — opt-in rule fired by default", in, norm(in))
		}
	}
}

func TestNormalize_optInWhenEnabled(t *testing.T) {
	n := newNormalizer(Options{NormalizePtr: true, NormalizeHex: true, NormalizeDate: true})
	if got := n.line("ptr 0xdeadbeef"); !strings.Contains(got, "<ADDR>") {
		t.Errorf("NormalizePtr: got %q", got)
	}
	if got := n.line("released 2026-07-11"); !strings.Contains(got, "<DATE>") {
		t.Errorf("NormalizeDate: got %q", got)
	}
}

func TestNormalize_rawIsIdentity(t *testing.T) {
	in := "\x1b[31m2026-07-11T21:00:00Z done in 5ms\x1b[0m"
	if got := newNormalizer(Options{Raw: true}).line(in); got != in {
		t.Errorf("raw mode altered the line: %q", got)
	}
}

// The safety test: a real semantic change on a line that ALSO carries every kind
// of noise token must still surface in the diff (never swallowed by
// normalization). This is the direct guard on "never hide a real change".
func TestNormalize_realChangeSurvivesNoise(t *testing.T) {
	cases := []struct{ prev, cur string }{
		{"2026-07-11T21:00:00Z FAIL auth_test 3 assertions", "2026-07-11T22:00:00Z FAIL auth_test 4 assertions"}, // count changed under a timestamp
		{"ok pkg 0.5s (0 failures)", "ok pkg 1.2s (2 failures)"},                                                 // failures changed under a duration
		{"goroutine 5: want 200 got 200", "goroutine 9: want 200 got 500"},                                       // value changed under a goroutine id
		{"/tmp/x/out: expected foo", "/tmp/x/out: expected bar"},                                                 // expectation changed under a temp path
	}
	for _, c := range cases {
		p := bigRun(1, c.prev)
		cur := bigRun(1, c.cur)
		r := Diff(&p, cur, 0, "k", Options{})
		if *r.Added < 1 || *r.Removed < 1 {
			t.Errorf("real change hidden by normalization:\n  prev=%q\n  cur =%q\n  added=%d removed=%d",
				c.prev, c.cur, *r.Added, *r.Removed)
		}
	}
}

// Regression tests for the over-masking bugs the adversarial review found: each
// of these once collapsed to the same normalized key, hiding the real change.
func TestNormalize_overMaskingRegressions(t *testing.T) {
	cases := []struct {
		name     string
		a, b     string
		wantSame bool // true = should normalize equal (noise); false = real change must survive
	}{
		{"min-as-minimum", "workers: 5 min, 20 max", "workers: 8 min, 20 max", false},
		{"tmp-glued-comma", "dir=/tmp/aaa,rc=0", "dir=/tmp/bbb,rc=1", false},
		{"tmp-glued-eq", "path=/tmp/x&code=0", "path=/tmp/y&code=1", false},
		{"pytest-of-status", "/scratch/pytest-of-ci/run:FAIL", "/scratch/pytest-of-ci/run:PASS", false},
		{"ipv6-port-cancels", "on [::1]:54321", "on [::1]:54999", true},
		{"tmp-path-still-cancels", "wrote /tmp/build-aaaa/o", "wrote /tmp/build-bbbb/o", true},
	}
	for _, c := range cases {
		na, nb := norm(c.a), norm(c.b)
		if c.wantSame && na != nb {
			t.Errorf("%s: expected noise to cancel but %q→%q != %q→%q", c.name, c.a, na, c.b, nb)
		}
		if !c.wantSame && na == nb {
			t.Errorf("%s: real change HIDDEN — %q and %q both normalize to %q", c.name, c.a, c.b, na)
		}
	}
}
