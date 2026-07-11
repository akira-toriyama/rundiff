package runner

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func shOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
}

func TestRun_capturesCombinedOutputInOrder(t *testing.T) {
	shOrSkip(t)
	// stdout then stderr then stdout — combined must preserve the write order.
	res, err := Run(context.Background(), []string{"sh", "-c", "printf 'out1\\n'; printf 'err1\\n' >&2; printf 'out2\\n'"})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.Exit != 0 {
		t.Errorf("Exit = %d, want 0", res.Exit)
	}
	got := string(res.Output)
	want := "out1\nerr1\nout2\n"
	if got != want {
		t.Errorf("Output = %q, want %q", got, want)
	}
}

func TestRun_nonzeroExitIsNotAnError(t *testing.T) {
	shOrSkip(t)
	res, err := Run(context.Background(), []string{"sh", "-c", "echo boom; exit 7"})
	if err != nil {
		t.Fatalf("Run returned error for a non-zero exit (should be a normal Result): %v", err)
	}
	if res.Exit != 7 {
		t.Errorf("Exit = %d, want 7", res.Exit)
	}
	if string(res.Output) != "boom\n" {
		t.Errorf("Output = %q, want %q", res.Output, "boom\n")
	}
}

func TestRun_commandNotFound(t *testing.T) {
	_, err := Run(context.Background(), []string{"rundiff-no-such-binary-xyzzy"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRun_emptyArgv(t *testing.T) {
	_, err := Run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected an error for empty argv")
	}
}

func TestRun_contextCancellationIsCancelled(t *testing.T) {
	shOrSkip(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	_, err := Run(ctx, []string{"sh", "-c", "sleep 5"})
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
}

func TestRun_contextTimeoutIsCancelled(t *testing.T) {
	shOrSkip(t)
	// A process killed because the context expired mid-run is an interrupt, not a
	// normal signalled result: it must be ErrCancelled so the caller does not
	// overwrite the baseline with a truncated capture.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := Run(ctx, []string{"sh", "-c", "sleep 5"})
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled for a context-killed process", err)
	}
}

func TestRun_selfSignalIsNormalResult(t *testing.T) {
	shOrSkip(t)
	// A process that dies of its OWN signal (context NOT cancelled) is a normal
	// Result with a negative exit — not an error.
	res, err := Run(context.Background(), []string{"sh", "-c", "kill -TERM $$"})
	if err != nil {
		t.Fatalf("Run returned error for a self-signalled process: %v", err)
	}
	if res.Exit >= 0 {
		t.Errorf("Exit = %d, want negative for a signal death", res.Exit)
	}
}

func TestGitBranch_nonRepoIsEmpty(t *testing.T) {
	// A fresh temp dir is not a git repo → "".
	if b := GitBranch(context.Background(), t.TempDir()); b != "" {
		t.Errorf("GitBranch = %q, want empty for a non-repo", b)
	}
}
