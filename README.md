# rundiff

[日本語](README.ja.md)

**Diff a command's output against its previous run — `fixed` / `new` / `unchanged` — for AI coding agents.**

In a fix → test → fix loop, a coding agent re-reads the *same* 50 KB of test
output every iteration and eyeball-diffs it to find what changed. rundiff does
that diff for you: it runs the command, and on a re-run prints **only what
changed since last time**.

Where [`pare`](https://github.com/akira-toriyama/pare) cuts in the *space*
direction (one run's output down to a budget), rundiff cuts in the *time*
direction (between runs). They compose.

```console
$ rundiff -- go test ./...          # first run: establishes a baseline, echoes output
$ rundiff -- go test ./...          # re-run: only the delta
── delta · fixed  exit=0 (prev 1)  +0 −1 ~214  churn=0.005  age=42s ──
- FAIL: TestParse/negative (0.00s)
```

## Why it's not just `diff`

A naive `diff <(cmd) cache` is useless on real command output: timestamps,
elapsed times, temp paths, ANSI color and **test ordering** change every run, so
everything looks different. rundiff's diff is:

- **Order-independent.** It compares the *multiset* of lines, so a test runner
  that prints its cases in a different order each run reports **zero changes**. A
  line that merely moved counts as unchanged.
- **Normalized.** Timestamps, durations, temp paths (`/tmp/…`, `/var/folders/…`),
  ANSI escapes, goroutine ids, UUIDs and ephemeral ports are canonicalized
  *before* comparison, so they don't show up as noise. Anything that could be
  real asserted data (bare numbers, dates, `0x…` values, git shas) is left alone
  by default — rundiff biases hard toward **never hiding a real change**.
- **Honest.** When the delta can't be trusted (binary output, huge churn, torn
  parallel output) rundiff *degrades* to a bounded full view and says so in the
  `degraded` / `degrade_reason` fields. It never prints a delta it doesn't trust.

## Install

rundiff is distributed as a Homebrew **cask**, a Nix flake, and from source. See
the [latest release](https://github.com/akira-toriyama/rundiff/releases) for
prebuilt binaries.

```sh
# Homebrew (macOS/Linux)
brew install akira-toriyama/tap/rundiff

# Nix
nix run github:akira-toriyama/rundiff -- --help

# From source (Go 1.26+)
go install github.com/akira-toriyama/rundiff/cmd/rundiff@latest
```

## Usage

```console
$ rundiff [flags] -- <command> [args...]
```

The first run for a given key **is** the baseline (it echoes the command's
output and records it). Every later run for the same key prints the delta. The
key is `argv + cwd + git branch`, so switching branches or changing the command
starts a fresh baseline.

Line 1 is **always** a machine-readable JSON object. In default mode a human/agent
delta body follows; with `--json` the whole record is that one object.

| flag | meaning |
|---|---|
| `--json` | emit the whole record as one JSON object (`added_lines`/`removed_lines` arrays instead of a text body) |
| `--raw` | compare raw lines with no noise-cancelling normalization |
| `--full` | show the bounded full current output as the body, even when a trusted delta exists |
| `--churn <0..1>` | degrade to full output when the changed fraction reaches this (default `0.5`) |

### The JSON contract

Line 1 is a single JSON object (schema `v:1`). The count fields are `null` when
no trustworthy line diff was computed (baseline, or a degrade that nulls counts).

| field | type | meaning |
|---|---|---|
| `v` | int | schema version |
| `key` | string | 12-hex prefix of the cache key (which baseline) |
| `exit` | int | the wrapped command's exit code (`-1` = signal) |
| `prev_exit` | int \| null | the baseline's exit code (`null` on baseline) |
| `transition` | enum | `baseline` \| `still_passing` \| `still_failing` \| `fixed` \| `regressed` — from the exit pair, always trustworthy |
| `degraded` | bool | `true` ⇒ the body is bounded full output, not a delta |
| `degrade_reason` | enum \| null | `binary` \| `too_large` \| `interleave` \| `small_output` \| `high_churn` |
| `added` / `removed` / `unchanged` | int \| null | line counts (multiset; `unchanged` includes moved lines) |
| `churn` | float \| null | `(added+removed)/(added+removed+unchanged)` |
| `total_prev` / `total_cur` | int \| null | normalized line counts |
| `baseline_age_s` | int \| null | seconds since the baseline was recorded |
| `normalized` | bool | `false` under `--raw` |
| `truncated` | bool | body/arrays clipped by a budget |
| `added_lines` / `removed_lines` | []string | `--json`, non-degraded: raw representative lines |
| `body` | string | `--json`, degraded/baseline: the bounded full output |

## Exit codes

rundiff **propagates the wrapped command's exit code**, and reserves conventional
high codes for its own failures:

| code | meaning |
|---|---|
| `0..255` | the wrapped command's exit code (propagated) |
| `125` | rundiff's own error (bad flags, cache/IO failure) |
| `126` | the command was found but is not executable |
| `127` | the command was not found |
| `130` | interrupted (Ctrl-C) before the run completed |

A propagated `125`/`126`/`127` is distinguishable from rundiff's own by the
JSON line: rundiff's own errors print to **stderr** and emit **no** JSON line.

## Composing with pare

rundiff shows *what changed*; on a baseline or a degrade it shows full output,
which can be large. Pipe that through [`pare`](https://github.com/akira-toriyama/pare)
to bound it — they cut on different axes:

```sh
rundiff --full -- go test ./... | pare --profile test
```

## Configuration

- `RUNDIFF_CACHE_DIR` overrides the cache directory. Otherwise rundiff uses
  `$XDG_CACHE_HOME/rundiff` (when absolute) or `~/.cache/rundiff`.

## More

- [docs/algorithm.md](docs/algorithm.md) — the normalization ruleset, degrade
  predicate, and JSON contract in detail.
- [docs/non-goals.md](docs/non-goals.md) — what rundiff deliberately does not do.
