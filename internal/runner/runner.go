// Package runner executes the wrapped command and captures what rundiff needs to
// diff it against its previous run: the combined stdout+stderr (in the order the
// process produced it) and the exit code. It is the process-spawning adapter —
// the pure diff logic lives in internal/delta and never runs a subprocess.
//
// A command that runs and exits non-zero is NOT an error here: that is a normal
// Result with a non-zero Exit (rundiff propagates it). Only a command that could
// not be started (not found, not executable) is returned as an error, which the
// CLI maps to the conventional 127/126.
package runner

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"strings"
)

// Result is one execution of the wrapped command.
type Result struct {
	// Output is the combined stdout+stderr, interleaved in the order the process
	// wrote it (os/exec shares one pipe when Stdout == Stderr, so there is no
	// reordering and no data race).
	Output []byte
	// Exit is the process exit code (0 on success). It is -1 when the process was
	// terminated by a signal (e.g. the context was cancelled by Ctrl-C).
	Exit int
}

// ErrNotFound reports that the command's executable could not be located.
var ErrNotFound = errors.New("command not found")

// ErrNotExecutable reports that the path exists but is not executable.
var ErrNotExecutable = errors.New("command not executable")

// ErrCancelled reports that the run was interrupted (context cancelled, e.g.
// Ctrl-C). The caller should not treat the partial capture as a new baseline.
var ErrCancelled = errors.New("command cancelled")

// Run executes argv (argv[0] is the program, the rest its arguments), capturing
// combined output and the exit code. ctx cancellation kills the process, so a
// Ctrl-C at the top propagates down.
func Run(ctx context.Context, argv []string) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("no command given")
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var buf bytes.Buffer
	// Same writer for both → os/exec uses one child pipe, preserving interleave
	// order and avoiding a concurrent-write race on the buffer.
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	res := Result{Output: buf.Bytes(), Exit: 0}

	if err == nil {
		return res, nil
	}

	// Interrupted (Ctrl-C): the context was cancelled. This must be checked BEFORE
	// the *exec.ExitError branch — when the context kills an already-started child,
	// cmd.Run returns an *exec.ExitError ("signal: killed", ExitCode -1), NOT the
	// context error, so classifying by ExitError first would mistake an interrupt
	// for a normal signalled run and let the truncated capture overwrite the good
	// baseline (runner's invariant: the partial capture must not become the baseline).
	if ctx.Err() != nil {
		return Result{}, ErrCancelled
	}

	// The command ran but exited non-zero (or died of its OWN signal): a normal
	// Result, not a rundiff error. ExitCode() is -1 for signal termination.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.Exit = exitErr.ExitCode()
		return res, nil
	}

	// The command could not be started. Classify to the conventional 127 (not
	// found) / 126 (not executable) by the underlying cause:
	//   - a bare name not on PATH surfaces as exec.ErrNotFound (LookPath), which
	//     errors.Is finds by unwrapping through the *exec.Error;
	//   - a path containing a slash skips PATH lookup, so a missing target fails
	//     at StartProcess with an *fs.PathError wrapping ENOENT (fs.ErrNotExist) —
	//     still "not found" (a typo'd/renamed script, wrong cwd), not 126.
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return Result{}, ErrNotFound
	}
	// Anything else that prevented start (permission denied, ENOEXEC, a directory,
	// …) is a present-but-not-runnable target → 126.
	return Result{}, ErrNotExecutable
}

// GitBranch returns the current git branch of dir, or "" when dir is not a git
// repository (or git is unavailable). Best-effort: it never fails the run — the
// branch only refines the cache key so a checkout switch starts a fresh baseline.
func GitBranch(ctx context.Context, dir string) string {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
