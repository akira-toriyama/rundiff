# CLAUDE.md — rundiff

rundiff is a small **command wrapper** (Go): it runs a command, captures its
combined output, and on a re-run for the same key (`argv + cwd + git branch`)
prints only what changed — an order-independent diff of normalized lines. Sister
to [`pare`](https://github.com/akira-toriyama/pare) (space-direction) on the time
axis. See [README.md](README.md) for behavior and
[docs/algorithm.md](docs/algorithm.md) for the diff/normalize/degrade spec.

## Layout (dependency rule: cmd → cli → {delta, adapter, hook, cache, runner, version}; delta, adapter and hook import nothing local)

- `cmd/rundiff/main.go` — thin entry; just `os.Exit(cli.Execute())`.
- `internal/delta` — the **pure** core (no I/O, no clock, no globals). Normalize,
  multiset diff, transition, degrade, render. Fuzzed. Do not add I/O here; feed it
  values, don't let it reach for them.
- `internal/adapter` — the second pure leaf (a twin of delta: same rules, no
  import between them — they mirror value shapes, the CLI carries values
  across). Recognizes tool output (go test / pytest / jest / vitest /
  cargo test / tsc / eslint) and extracts the file-level
  `failing`/`fixed`/`new` claim. Bias: **when unsure, say nothing** (nil) — a
  false `fixed` stops an agent looking. Fuzzed.
- `internal/hook` — the third pure leaf: decides whether a Claude Code Bash
  command can be auto-wrapped with `rundiff --`, and renders the settings
  snippet. Bias, the adapter's transposed: **when unsure, do not rewrite** — a
  wrong rewrite silently changes what command runs. It emits DIRECT argv (never
  `bash -lc`), because a shell wrapper blinds the adapter's whole-token gates.
  Fuzzed, including against a real `/bin/sh`.
- `internal/runner` — runs the wrapped command (combined capture, exit code,
  git branch). The only subprocess adapter.
- `internal/cache` — per-key baseline persistence (XDG dir, atomic writes, stores
  **raw** output so the diff re-normalizes each run).
- `internal/cli` — cobra adapter: flags, wiring, the exit-code contract.
- `internal/version` — build identity (ldflags-injected, VCS fallback).

## Conventions

- **Verify with `sh scripts/check.sh`** (module hygiene / build / vet /
  `test -race` / docs guard / lint / vulncheck / smoke). Green here ⇒ green CI.
- **Exit codes:** rundiff *propagates* the wrapped command's exit code, and
  reserves `125` (own error) · `126` (not executable) · `127` (not found) · `130`
  (interrupted). rundiff's own errors go to **stderr** and emit no JSON line.
  stdout is a machine API, and *which* one is chosen by the command: in wrapper
  mode (`rundiff -- <cmd>`) line 1 is always the run record; `rundiff hook
  rewrite` speaks Claude Code's hook schema instead and is **total** — it wraps
  nothing, so those codes do not apply and it always exits 0 (never 2, which
  would block the agent's command; a hook that cannot decide writes nothing).
- **Safety rule:** normalization must never hide a real change. New rules go in
  the default-ON set only if they cancel provably run-varying chrome; anything
  that could be asserted data is opt-in. Add a case to
  `TestNormalize_realChangeSurvivesNoise` for any new rule. Adapter clause:
  **never claim what isn't proven** — a wrong `fixed` is worse than no claim.
  A new or changed parser must keep `TestExtract_neverFalseFixed` (the
  line-deletion sweep) green and reconcile against the tool's own counts.
  Hook clause: **never rewrite what cannot be expressed as direct argv.** A new
  or changed rewrite rule must keep `TestRewrite_neverWrapsWhatItCannotExpress`
  (the metacharacter sweep) green, and `hook.Targets()` must stay in sync with
  `adapter.Tools()` (`TestHookTargets_coverAdapterTools`).
- **Adapter fixtures are real captures.** Stage new candidates with
  `sh scripts/gen-adapter-fixtures.sh [tool[@version] …]` (dev-only, never CI;
  it writes to `tmp/adapter-fixtures`). Review each transcript, copy keepers
  into `internal/adapter/testdata/captures/` (era prefix like `v8-fail.out`),
  and record provenance in that tool's `VERSIONS` — never commit blind.
- **Commits:** gitmoji + Conventional Commits
  ([CONTRIBUTING](https://github.com/akira-toriyama/.github/blob/main/CONTRIBUTING.md)).
  Enable the hook: `git config core.hooksPath scripts/hooks`.
- **Docs:** keep README.md and README.ja.md in sync on any user-visible change,
  both **version-agnostic** (link to Releases, never hardcode a release number).
- **Third-party GitHub Actions are pinned to a commit SHA** with a `# vX` comment.

## Releasing

Tag `vX.Y.Z` and push → `.github/workflows/release.yml` runs git-cliff +
GoReleaser (binaries, checksums, Homebrew cask, build-provenance). The cask push
needs `HOMEBREW_TAP_TOKEN`; without it the release still succeeds and skips only
the cask. Bump nothing by hand — the version is ldflags-injected at tag time.

Note: `flake.nix`'s `vendorHash` pins the vendored go modules. When go.mod/go.sum
change, set it to `pkgs.lib.fakeHash`, run `nix build`, and paste the hash nix
prints (CI does not nix-build, so this is maintained out of band).

## Task tracking

Work is tracked in the central `projects` furrow board, scoped to this repo.
From inside this checkout: `furrow next` / `furrow ls`. PRs may carry a
`SetStatus-task:` footer to move a task's status on open/merge.

## Fleet-managed files (do not hand-edit here)

`.github/workflows/{task-status,commit-lint,taplo,zizmor}.yml`,
`.github/dependabot.yml`, `.github/zizmor.yml`, and `docs/commit-convention.md`
are distributed by the org `.github` repo's fleet-sync and overwritten on its
next run — edit the canonical copies there.
