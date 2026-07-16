# Non-goals

What rundiff deliberately does **not** do, so the tool stays small and composable.

## Non-goals

- **Space-direction truncation.** rundiff does not budget-bound a single run's
  output — that is [`pare`](https://github.com/akira-toriyama/pare)'s job. On a
  baseline or a degrade rundiff emits full output; pipe it through pare
  (`rundiff --full -- … | pare`). The two cut on different axes and compose.
- **Live pass-through / streaming.** rundiff buffers the command's output, then
  prints the delta — it is not a `tail -f`. For an agent that waits for the run
  to finish anyway, this is the point (the raw output is replaced by the delta).
- **Summarization / rewriting.** rundiff selects and counts lines; it never
  collapses repeats or rewrites text. The displayed lines are always verbatim raw
  input lines.
- **Hiding a real change to reduce noise.** The safety bias is absolute:
  normalization only cancels tokens that are provably run-varying chrome. Bare
  numbers, dates, `0x…` values and git shas are left alone by default even though
  they add noise, because masking them could hide a real diff.

- **Multi-tool composite runs.** The file-level adapter claims a run only when
  exactly ONE tool's output fingerprint matches. A script that runs tsc *and*
  eslint in one invocation is ambiguous — attributing identities across two
  interleaved formats risks a false claim, so the adapter stays silent. Wrap
  the tools separately to get per-tool claims.

- **Auto-wrapping arbitrary commands.** The `PreToolUse` hook rewrites only a
  metacharacter-free, direct-argv invocation of a recognized test tool. Every
  refusal is load-bearing, not laziness: rundiff *replaces* a command's stdout
  with a record plus a delta, so anything whose stdout is read downstream (a
  pipe, a redirect, a `$(…)`) must not be wrapped; `npm test`'s script body is
  invisible and may be a watcher, and a wrapped watcher hangs forever with no
  output; a quoted or globbed argument cannot be re-emitted as direct argv
  without a shell, and rewriting into `bash -lc` would blind the adapter's
  argv gates (see docs/algorithm.md A7/A9). When unsure, the hook does not
  rewrite — the command simply runs unwrapped.

- **Writing your Claude Code config.** `rundiff hook print` emits the snippet to
  stdout; merging it is yours (or your dotfiles') to do. A tool that edits
  `~/.claude/settings.json` owns a file it did not create, next to a permission
  allowlist a bad write would destroy, on a machine whose state it cannot
  reproduce.

## Deferred (candidates for a later version)

- **Adapter parsers for more tools / more output eras.** The adapter recognizes
  go test, pytest, jest, vitest, cargo test, tsc and eslint, with fixtures
  captured from real runs across multiple eras of each (see
  `internal/adapter/testdata/captures/*/VERSIONS` for per-tool provenance;
  regenerate with `scripts/gen-adapter-fixtures.sh`). Format drift in a future
  tool version fails the fingerprint or the count reconciliation and the
  adapter abstains — never lies — but re-capturing new eras (and adding tools)
  is ongoing work.
- **`--baseline <id>` history.** Today rundiff keeps one baseline per key (the
  last run). Pinning an older comparison point needs a per-key run history.
- **Package scripts (`npm test`) in the hook.** Reading the script body out of
  `package.json` would let the hook wrap the command agents most often type — but
  it must first prove the body is not a watcher, and a pure leaf cannot read a
  file. Deliberately forfeited for now: a wrapped watcher hangs with no output,
  which is a far worse failure than not wrapping.
- **Quoted arguments in the hook** (`pytest -k "a or b"`). Surviving the
  round-trip needs a shell-faithful tokenizer *and* re-quoter. Largely
  self-cancelling: those are close to exactly the runs the adapter's selection
  gates (A7) already refuse to claim on.
- **Env-prefixed commands in the hook** (`GOFLAGS=… go test`). Hoisting the
  prefix is easy; the problem is that the environment is not part of the cache
  key, so two different selections would collide on one baseline. The key has to
  grow first.
- **CLI flags for the per-rule escapes and opt-in normalization**
  (`--no-time`, `--normalize-ptr`, …). They exist in the core `Options`; only a
  curated subset (`--json`, `--raw`, `--full`, `--churn`) is wired to the CLI.
- **A man page.** `rundiff --help` (cobra `Example:` blocks) and
  `rundiff completion <shell>` cover the ergonomics without pulling a
  markdown→roff dependency into the supply chain.
