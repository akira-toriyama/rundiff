package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runCLI drives the root command with args and returns stdout and the mapped
// exit code (mirroring Execute's error→code mapping).
func runCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	return runCLICtx(t, context.Background(), args...)
}

// runCLICtx is runCLI under a caller-supplied context, so a test can drive the
// interrupt path with a pre-cancelled context.
func runCLICtx(t *testing.T, ctx context.Context, args ...string) (string, int) {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return out.String(), 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return out.String(), ee.code
	}
	return out.String(), codeRundiff
}

// bigContent joins lines plus filler so the output clears the small-output
// degrade threshold, exercising the trusted-delta path.
func bigContent(lines ...string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	for i := 0; b.Len() <= 2200; i++ {
		fmt.Fprintf(&b, "constant filler line number %05d\n", i)
	}
	return b.String()
}

func setCache(t *testing.T) {
	t.Helper()
	t.Setenv("RUNDIFF_CACHE_DIR", t.TempDir())
}

func TestVersionSubcommand(t *testing.T) {
	out, code := runCLI(t, "version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.HasPrefix(out, "rundiff ") {
		t.Errorf("version output = %q, want it to start with 'rundiff '", out)
	}
}

func TestBaselineThenRerunThenChange(t *testing.T) {
	if _, err := os.Stat("/bin/cat"); err != nil {
		t.Skip("/bin/cat unavailable")
	}
	setCache(t)
	f := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(f, []byte(bigContent("alpha", "beta", "gamma")), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1) baseline
	out1, code1 := runCLI(t, "--json", "--", "cat", f)
	if code1 != 0 {
		t.Fatalf("baseline exit = %d, want 0", code1)
	}
	if !strings.Contains(out1, `"transition":"baseline"`) {
		t.Errorf("baseline output missing baseline transition: %s", out1)
	}

	// 2) identical re-run → still_passing, nothing added
	out2, _ := runCLI(t, "--json", "--", "cat", f)
	if !strings.Contains(out2, `"transition":"still_passing"`) {
		t.Errorf("rerun output missing still_passing: %s", out2)
	}
	if !strings.Contains(out2, `"added":0`) || !strings.Contains(out2, `"removed":0`) {
		t.Errorf("identical rerun should have added:0 removed:0: %s", out2)
	}
	if !strings.Contains(out2, `"degraded":false`) {
		t.Errorf("large identical output should not degrade: %s", out2)
	}

	// 3) one changed line → one added, one removed
	if err := os.WriteFile(f, []byte(bigContent("alpha", "CHANGED", "gamma")), 0o644); err != nil {
		t.Fatal(err)
	}
	out3, _ := runCLI(t, "--json", "--", "cat", f)
	if !strings.Contains(out3, `"added":1`) || !strings.Contains(out3, `"removed":1`) {
		t.Errorf("changed rerun should have added:1 removed:1: %s", out3)
	}
	if !strings.Contains(out3, `"removed_lines":["beta"]`) {
		t.Errorf("changed rerun should list the removed raw line 'beta': %s", out3)
	}
}

func TestJSONFullIncludesBody(t *testing.T) {
	if _, err := os.Stat("/bin/cat"); err != nil {
		t.Skip("/bin/cat unavailable")
	}
	setCache(t)
	f := filepath.Join(t.TempDir(), "data")
	os.WriteFile(f, []byte(bigContent("one", "two", "three")), 0o644)
	runCLI(t, "--json", "--full", "--", "cat", f) // baseline
	os.WriteFile(f, []byte(bigContent("one", "TWO", "three")), 0o644)
	out, _ := runCLI(t, "--json", "--full", "--", "cat", f)
	// A trusted re-run with --json --full must carry the full output in "body",
	// not silently drop it and emit empty delta arrays.
	if !strings.Contains(out, `"body":`) {
		t.Errorf("--json --full dropped the body: %s", out)
	}
	if strings.Contains(out, `"added_lines":[]`) {
		t.Errorf("--json --full emitted contradictory empty delta arrays: %s", out)
	}
}

func TestExitCodePropagation(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh unavailable")
	}
	setCache(t)
	_, code := runCLI(t, "--", "sh", "-c", "exit 3")
	if code != 3 {
		t.Errorf("exit code = %d, want 3 (propagated from the wrapped command)", code)
	}
}

func TestCommandNotFound(t *testing.T) {
	setCache(t)
	_, code := runCLI(t, "--", "rundiff-no-such-command-xyzzy")
	if code != codeNotFound {
		t.Errorf("exit code = %d, want %d (not found)", code, codeNotFound)
	}
}

func TestNoCommand(t *testing.T) {
	_, code := runCLI(t)
	if code != codeRundiff {
		t.Errorf("exit code = %d, want %d for no command", code, codeRundiff)
	}
}

func TestChurnOutOfRange(t *testing.T) {
	// Both arms of the 0..1 validation.
	for _, v := range []string{"2", "-0.5"} {
		if _, code := runCLI(t, "--churn", v, "--", "true"); code != codeRundiff {
			t.Errorf("--churn %s: exit code = %d, want %d (out of range)", v, code, codeRundiff)
		}
	}
}

func TestChurnZeroDegradesOnAnyChange(t *testing.T) {
	if _, err := os.Stat("/bin/cat"); err != nil {
		t.Skip("/bin/cat unavailable")
	}
	setCache(t)
	f := filepath.Join(t.TempDir(), "data")
	os.WriteFile(f, []byte(bigContent("a", "b", "c")), 0o644)
	runCLI(t, "--churn", "0", "--", "cat", f) // baseline
	os.WriteFile(f, []byte(bigContent("a", "X", "c")), 0o644)
	out, _ := runCLI(t, "--json", "--churn", "0", "--", "cat", f)
	// --churn 0 means "degrade on any change" — an explicit 0 must reach the core
	// (not collide with the unset default 0.5).
	if !strings.Contains(out, `"degraded":true`) || !strings.Contains(out, `"degrade_reason":"high_churn"`) {
		t.Errorf("--churn 0 should degrade (high_churn) on any change: %s", out)
	}
}

func TestNotExecutableExitCode(t *testing.T) {
	setCache(t)
	f := filepath.Join(t.TempDir(), "not-exec")
	if err := os.WriteFile(f, []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code := runCLI(t, "--", f); code != codeNotExecutable {
		t.Errorf("exit code = %d, want %d (not executable)", code, codeNotExecutable)
	}
}

func TestInterruptedExitCode(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh unavailable")
	}
	setCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled → the run is interrupted before it can complete
	if _, code := runCLICtx(t, ctx, "--", "sh", "-c", "sleep 5"); code != codeInterrupted {
		t.Errorf("exit code = %d, want %d (interrupted)", code, codeInterrupted)
	}
}

func TestSignalDeathPropagatesAs128(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh unavailable")
	}
	setCache(t)
	// A wrapped command that dies of its own signal (exit -1) maps to 128.
	if _, code := runCLI(t, "--", "sh", "-c", "kill -TERM $$"); code != 128 {
		t.Errorf("exit code = %d, want 128 (signal death)", code)
	}
}

func TestCorruptBaselineTreatedAsFresh(t *testing.T) {
	if _, err := os.Stat("/bin/cat"); err != nil {
		t.Skip("/bin/cat unavailable")
	}
	dir := t.TempDir()
	t.Setenv("RUNDIFF_CACHE_DIR", dir)
	f := filepath.Join(t.TempDir(), "data")
	os.WriteFile(f, []byte(bigContent("a", "b", "c")), 0o644)

	runCLI(t, "--", "cat", f) // establish a baseline (writes one cache file)
	cacheFiles, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(cacheFiles) == 0 {
		t.Fatal("no cache file was created by the baseline run")
	}
	for _, cf := range cacheFiles { // corrupt it
		os.WriteFile(cf, []byte("{not valid json"), 0o644)
	}

	// The corrupt baseline must not wedge the run: it is treated as absent, so the
	// next run cleanly re-establishes a baseline (exit 0, transition=baseline).
	out, code := runCLI(t, "--json", "--", "cat", f)
	if code != 0 {
		t.Fatalf("corrupt baseline wedged the run: exit=%d", code)
	}
	if !strings.Contains(out, `"transition":"baseline"`) {
		t.Errorf("corrupt baseline should be treated as absent (fresh baseline): %s", out)
	}
}

func TestDefaultModeTextBody(t *testing.T) {
	if _, err := os.Stat("/bin/cat"); err != nil {
		t.Skip("/bin/cat unavailable")
	}
	setCache(t)
	f := filepath.Join(t.TempDir(), "data")
	os.WriteFile(f, []byte(bigContent("one", "two", "three")), 0o644)
	runCLI(t, "--", "cat", f) // baseline
	os.WriteFile(f, []byte(bigContent("one", "TWO", "three")), 0o644)
	out, _ := runCLI(t, "--", "cat", f)
	// Line 1 is the JSON object; the text body shows the -/+ change.
	if !strings.Contains(out, `"transition":"still_passing"`) {
		t.Errorf("default-mode line 1 should be JSON: %s", out)
	}
	if !strings.Contains(out, "\n- two") || !strings.Contains(out, "\n+ TWO") {
		t.Errorf("default-mode body should show -/+ lines:\n%s", out)
	}
}

// stubScript writes an executable that prints the given file's content and
// exits with the given code — a deterministic stand-in for a wrapped tool.
func stubScript(t *testing.T, output string, exit int) string {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(data, []byte(output), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "tool.sh")
	body := fmt.Sprintf("#!/bin/sh\ncat %q\nexit %d\n", data, exit)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// gotestOutput fabricates go-test-shaped output large enough to clear the
// small-output degrade (the claim must be independent of the line diff's fate).
func gotestOutput(fail bool) string {
	var b strings.Builder
	if fail {
		b.WriteString("--- FAIL: TestMul (0.00s)\n    calc_test.go:13: Mul(2,3) = 6, want 7\n")
	}
	for i := 0; i < 150; i++ {
		fmt.Fprintf(&b, "=== RUN   TestFiller%05d\n", i)
	}
	if fail {
		b.WriteString("FAIL\nFAIL\texample.com/fix/calc\t0.20s\nok  \texample.com/fix/str\t0.10s\nFAIL\n")
	} else {
		b.WriteString("PASS\nok  \texample.com/fix/calc\t0.21s\nok  \texample.com/fix/str\t0.10s\n")
	}
	return b.String()
}

// The end-to-end claim path: a failing baseline carries tool+failing with a
// null cross-run pair; the fixed re-run claims the package fixed. The argv must
// hint go-test, so the stub is invoked through a "go test" wrapper name.
func TestToolClaimEndToEnd(t *testing.T) {
	setCache(t)
	failing := stubScript(t, gotestOutput(true), 1)
	passing := stubScript(t, gotestOutput(false), 0)

	// argv[0] basename "go" + word "test" fires the hint; the script ignores
	// its args.
	goDir := t.TempDir()
	goStub := filepath.Join(goDir, "go")
	if err := os.WriteFile(goStub, []byte("#!/bin/sh\nexec \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	out1, code1 := runCLI(t, "--json", "--", goStub, failing, "test")
	if code1 != 1 {
		t.Fatalf("baseline exit = %d, want 1 (propagated)", code1)
	}
	for _, want := range []string{`"tool":"go-test"`, `"failing":["example.com/fix/calc"]`, `"fixed":null`, `"new":null`} {
		if !strings.Contains(out1, want) {
			t.Errorf("baseline line 1 missing %s: %s", want, out1)
		}
	}

	// Same key (same argv) — swap the stub target by rewriting the wrapper.
	if err := os.WriteFile(goStub, []byte(fmt.Sprintf("#!/bin/sh\nexec %q\n", passing)), 0o755); err != nil {
		t.Fatal(err)
	}
	out2, code2 := runCLI(t, "--json", "--", goStub, failing, "test")
	if code2 != 0 {
		t.Fatalf("re-run exit = %d, want 0", code2)
	}
	for _, want := range []string{`"tool":"go-test"`, `"failing":[]`, `"fixed":["example.com/fix/calc"]`, `"new":[]`} {
		if !strings.Contains(out2, want) {
			t.Errorf("re-run line 1 missing %s: %s", want, out2)
		}
	}
}

// --tool none disables the adapter: all four fields stay null.
func TestToolNone(t *testing.T) {
	setCache(t)
	failing := stubScript(t, gotestOutput(true), 1)
	goDir := t.TempDir()
	goStub := filepath.Join(goDir, "go")
	os.WriteFile(goStub, []byte("#!/bin/sh\nexec \"$1\"\n"), 0o755)
	out, _ := runCLI(t, "--json", "--tool", "none", "--", goStub, failing, "test")
	for _, want := range []string{`"tool":null`, `"failing":null`, `"fixed":null`, `"new":null`} {
		if !strings.Contains(out, want) {
			t.Errorf("--tool none line 1 missing %s: %s", want, out)
		}
	}
}

// An unknown --tool is rundiff's own error: exit 125, no JSON line.
func TestToolUnknown(t *testing.T) {
	setCache(t)
	out, code := runCLI(t, "--tool", "bogus", "--", "true")
	if code != codeRundiff {
		t.Fatalf("exit = %d, want %d", code, codeRundiff)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty (errors go to stderr, no JSON line)", out)
	}
}

// The claim channel is independent of the line diff: a forced degrade
// (--churn 0) still carries fixed/new in line 1.
func TestToolClaimSurvivesDegrade(t *testing.T) {
	setCache(t)
	failing := stubScript(t, gotestOutput(true), 1)
	passing := stubScript(t, gotestOutput(false), 0)
	goDir := t.TempDir()
	goStub := filepath.Join(goDir, "go")
	os.WriteFile(goStub, []byte("#!/bin/sh\nexec \"$1\"\n"), 0o755)

	runCLI(t, "--json", "--churn", "0", "--", goStub, failing, "test")
	os.WriteFile(goStub, []byte(fmt.Sprintf("#!/bin/sh\nexec %q\n", passing)), 0o755)
	out, _ := runCLI(t, "--json", "--churn", "0", "--", goStub, failing, "test")
	if !strings.Contains(out, `"degraded":true`) {
		t.Fatalf("expected a degraded run: %s", out)
	}
	if !strings.Contains(out, `"fixed":["example.com/fix/calc"]`) {
		t.Errorf("degraded line 1 lost the claim: %s", out)
	}
}

// An interrupted run (Ctrl-C, or the SIGTERM a tool timeout sends) reports the
// partial capture and does NOT become the baseline. Dropping the bytes would
// make rundiff strictly worse than not wrapping the command at all — an
// unwrapped kill still leaves the partial log — and caching them would let half
// a run become the comparison point for the next one.
func TestInterrupted_reportsPartialAndNeverBecomesBaseline(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh unavailable")
	}
	dir := t.TempDir()
	t.Setenv("RUNDIFF_CACHE_DIR", dir)

	script := filepath.Join(t.TempDir(), "slow.sh")
	if err := os.WriteFile(script,
		[]byte("#!/bin/sh\nprintf 'PASS pkg/a\\nFAIL pkg/b — assertion failed\\n'\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	out, code := runCLICtx(t, ctx, "--", script)

	if code != codeInterrupted {
		t.Errorf("exit code = %d, want %d (interrupted)", code, codeInterrupted)
	}
	for _, want := range []string{`"transition":"interrupted"`, `"degrade_reason":"interrupted"`, `"tool":null`, `"fixed":null`} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %s\ngot: %s", want, out)
		}
	}
	if !strings.Contains(out, "FAIL pkg/b") {
		t.Errorf("stdout must carry the lines printed before the kill, got: %s", out)
	}

	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("cache dir has %d entries, want 0: a partial capture must never become the baseline", len(entries))
	}
}
