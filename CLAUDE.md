# CLAUDE.md — rundiff

rundiff is a small **command wrapper** (Go): it runs a command, captures its
combined output, and on a re-run for the same key (`argv + cwd + git branch`)
prints only what changed — an order-independent diff of normalized lines. Sister
to [`pare`](https://github.com/akira-toriyama/pare) (space-direction) on the time
axis. See [README.md](README.md) for behavior and
[docs/algorithm.md](docs/algorithm.md) for the diff/normalize/degrade spec.

## Layout (dependency rule: cmd → cli → {delta, cache, runner, version}; delta imports nothing local)

- `cmd/rundiff/main.go` — thin entry; just `os.Exit(cli.Execute())`.
- `internal/delta` — the **pure** core (no I/O, no clock, no globals). Normalize,
  multiset diff, transition, degrade, render. Fuzzed. Do not add I/O here; feed it
  values, don't let it reach for them.
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
  (interrupted). rundiff's own errors go to **stderr** and emit no JSON line;
  stdout stays a clean machine API (line 1 is always the JSON record).
- **Safety rule:** normalization must never hide a real change. New rules go in
  the default-ON set only if they cancel provably run-varying chrome; anything
  that could be asserted data is opt-in. Add a case to
  `TestNormalize_realChangeSurvivesNoise` for any new rule.
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
