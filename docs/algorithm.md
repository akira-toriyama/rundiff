# rundiff — algorithm

The pure core (`internal/delta`) turns two runs' raw output + exit codes into a
report. It does no I/O, reads no clock, and imports no other local package — all
non-determinism (baseline age, cache key, flags) is injected. The pipeline:

```
raw prev/cur → split lines → normalize each line → multiset diff → degrade? → render
```

Normalization is applied *identically* to both runs and only ever builds a
**match key**; the diff always displays the raw line the key stood for (the
verbatim-subset invariant). The guiding principle is **safety: never hide a real
change.** Normalization that is too aggressive is worse than leaving noise in, so
anything that could be asserted data is left alone by default.

## Normalization ruleset (ordered, per line)

| stage | rule | canonicalizes | default |
|---|---|---|---|
| 0 | CR handling | trailing `\r` trimmed; interior `\r` progress frames collapse to the last segment | on |
| 1 | ANSI/ESC | CSI, OSC, DCS/PM/APC/SOS, nF escapes, lone ESC → removed | on |
| 3 | timestamps | ISO-8601 → `<TS>`; `HH:MM:SS(.ms)` → `<TIME>` (seconds required so `12:00` survives) | on (`--no-time` off) |
| 4 | durations | compound (`1h2m3s`), abbrev/dec-sec (`250ms`, `4.5s`), keyword-anchored ints (`in 4s`) → `<DUR>` | on (`--no-dur`) |
| 5 | temp paths | `/var/folders/…`, `/tmp/…`, random tmp basenames → `<TMP>` (stops at `:` so `file:line:col` survives) | on (`--no-tmp`) |
| 6 | identifiers | `goroutine N` → `goroutine <N>`; UUID → `<UUID>`; loopback `host:port` → `host:<PORT>` | on (`--no-uuid`, `--no-port`) |
| 7 | opt-in | `0x…` → `<ADDR>`; hex/sha → `<HEX>`; bare date → `<DATE>`; collapse spaces | **off** (`--normalize-*`, not yet wired to CLI) |
| 8 | rstrip | trailing spaces/tabs left by removals | on |

Deliberately **never** masked: bare integers (`expected 3 got 4`), bare
single-letter durations (`5m`), spelled-out durations (`5 minutes` — too easily
confused with data like `5 min`imum), and — by default — `0x…` values, git shas
and bare dates. Temp-path masks stop at delimiters (`,` `=` `]` `}` `(` `>` `&`
`|` quotes `:` `)`) so they never swallow run-varying data glued to a path. Each
rule is idempotent, total (never errors on arbitrary bytes), and single-line.

## Multiset diff

For each distinct normalized key with `N` occurrences in prev and `M` in cur:
`unchanged += min(N,M)`, `added += max(0, M−N)`, `removed += max(0, N−M)`.
Conservation holds exactly: `unchanged + removed == total_prev` and
`unchanged + added == total_cur`. Because counts are order-free, a reordered run
is all-unchanged. The displayed representative for a key is its
lexicographically-smallest raw line, so the rendered body is
permutation-invariant too.

The `transition` comes from the exit pair alone (`pass ⇔ exit == 0`) and is
therefore always trustworthy, even when the line diff is degraded.

## Degrade predicate (first-match-wins)

Degrading withholds the compact delta and prints bounded full current output
instead — it only ever shows *more*, so the predicates are biased to fire.

| # | reason | condition | counts |
|---|---|---|---|
| — | (baseline) | `prev == nil` | null |
| G1 | `binary` | a NUL byte, or >10% non-text over a 64 KiB head sample | null |
| G2 | `too_large` | either run > 8 MiB or > 50 000 lines (map never built) | null |
| G3 | `interleave` | a physical line > 8192 bytes (torn parallel output) | null |
| G4 | `small_output` | `min lines < 3` or `max bytes ≤ 2048` (a delta isn't clearer than the whole) | real |
| G5 | `high_churn` | `churn ≥ --churn` (default 0.5) | real |
| G6 | `too_large` | `added + removed > 2000` (unpasteable) | real |
| G7 | `normalization_uncertain` | delta ≥ 8 **and** a re-diff under the opt-in rules (0x/hex/date/collapse) shrinks it to ≤ half — the residue was noise the default rules missed | real |

G7 is the mid-churn safety net below G5. It re-diffs the same lines under a
stronger normalizer (the default rules **plus** the opt-in ones, with the caller's
escapes left untouched); if that collapses the delta to half or less, most of the
"change" was run-varying noise the default set failed to cancel, so the compact
delta is not trustworthy and rundiff shows bounded full output instead. This never
hides a change — degrading only ever shows *more*, and the probe's (individually
unsafe) normalized text is never displayed; only its delta *size* feeds the
decision. Tiny deltas (`< 8`) are skipped so a genuine small change is never degraded.

## Invariants (enforced by tests + fuzz)

Never panics on any bytes; nil ≡ empty; conservation; non-negativity; identity
(equal input ⇒ zero delta); duplicate correctness; order independence (counts and
rendered body); symmetry (`added(a,b) == removed(b,a)`); determinism (same inputs
⇒ byte-identical output); normalization idempotent/total/single-line; baseline
nulls the counts; `baseline_age_s ≥ 0`; verbatim-subset (every displayed line is a
raw input line). See `internal/delta/*_test.go` and `FuzzDiff`/`FuzzNormalize`.

## File-level adapter (`tool` / `failing` / `fixed` / `new`)

`internal/adapter` is a second pure leaf beside delta: it re-parses the raw
bytes of both runs (the cache stores raw output), recognizes one of seven tools
from **output fingerprints** (argv is a candidate filter, never proof), and
turns the pair into a file-level claim. Its safety stance inverts the line
diff's: a delta degrades — it shows *more* when unsure — but a claim cannot
degrade; its failure mode is a false statement, and a false `fixed:["x"]` makes
an agent stop looking. So **when unsure, the adapter says nothing**: `null`
(no claim) is distinct from `[]` (confidently none).

Identity per tool: file paths (pytest nodeids coarsened to the file; jest /
vitest suite paths; tsc / eslint diagnostic paths), the package import path for
`go test`, the `module::case` test name for `cargo test`.

### Gate pipeline (failure ⇒ silence at the stated scope)

| # | gate | condition for silence |
|---|---|---|
| A0 | kill switch | `--tool none` |
| A1 | input guards | run > 8 MiB, > 50 000 lines, or a NUL byte |
| A2 | selection | argv-hint-narrowed candidates (hints look only at the command position plus one launcher level — a tool name in a script name or path argument never narrows); **exactly one** parser's fingerprint may match (silent-clean exception: an empty exit-0 run is claimable by a `silentWhenClean` tool — tsc, eslint — adopted from the other run's parser plus an agreeing argv hint or `--tool`) |
| A3 | blocked flags | a flag — in argv or lifted from the environment (`GOFLAGS`, `PYTEST_ADDOPTS`) — that changes the tool's format or exit semantics (`go test -json`, `jest --watch`, `eslint -f`, `tsc --incremental`, …) |
| A4 | parse + reconcile | the tool's sentinel ("run finished" line) missing, or the extracted failing count disagrees with the tool's own summary counts |
| A5 | exit cross-check + cap | exit outside the tool's accepted set; `(exit==0) ⇎ (failing empty)`; signal exit; > 200 identities |
| A6 | comparability (pair) | baseline, unparsed previous run, or a different tool ⇒ `fixed`/`new` null (`failing` survives) |
| A7 | selection variance (pair) | a NAME-level selection flag (`go test -run`/`-skip`, pytest `-k`/`-m`/`--lf`, jest/vitest `-t`/`--onlyChanged`/`--changed`/`--shard`, a cargo name filter) in argv or the environment ⇒ `fixed`/`new` null — a rename or an unrelated edit silently deselects a still-failing test under identical argv, so a green run proves nothing |
| A8 | strict accounting (pair) | any previously-failing identity lacking positive evidence in the current run ⇒ `fixed`/`new` null together |

A8 is the load-bearing rule: **`fixed` is never inferred from absence.** For
the chatty tools (go test, pytest, jest, vitest, cargo) evidence is strictly
PER-IDENTITY — `ok pkg`, `PASS file`, an all-dots pytest progress line,
`✓ file` with a skip-free count, `test x ... ok` — because their identity
universe varies with selection, skips and config, so a green run does not
vouch for an identity that printed no line. Only the `silentWhenClean` tools
(tsc, eslint) get a global clean-run proof (exit 0 plus the tool's own
zero-failure output): their clean run is inherently whole-project and
markerless. An identity the tool positively reports as *not run* defeats any
evidence — skipping or deleting a failure is not fixing it: go's
`? pkg [no test files]` and `ok pkg [no tests to run]`, cargo's `... ignored`
(with or without a reason), a pytest progress line containing any `s`/`x`, a
vitest `↓` file or a `✓ … skipped` count. Tool-reported skip totals that
cannot be attributed to an identity (jest's `N skipped/todo`, a go `--- SKIP:`
mark, a pytest `N deselected` bar) drop ALL pass evidence for that run —
conservative, never a lie.

### Known limitations (documented residuals, all on the abstain-or-honest side except the first)

- **go non-verbose skips are invisible.** `go test` (without `-v`) prints an
  identical `ok pkg` whether every test ran or one was newly `t.Skip()`ed, so a
  skip-silenced failure inside a package reads as that *package* passing.
  Identities are package-granular for go; at that granularity the claim is
  true — but be aware a skip can hide inside it. `-v` runs are protected (the
  `--- SKIP:` mark drops pass evidence).
- **Config-driven exclusion is invisible.** A tsconfig `exclude`, an
  `.eslintignore` entry, jest `testPathIgnorePatterns`, pytest
  `collect_ignore` added between runs removes a file from the universe without
  an output trace. For the chatty tools the identity then simply loses its
  evidence (pair withheld); for tsc/eslint the global clean-run proof can call
  an excluded-but-still-broken file fixed. Exclusion edits change the config
  file, not argv, so rundiff cannot see them.
- **Env-driven selection is covered only for `GOFLAGS` and `PYTEST_ADDOPTS`**
  (lifted into the flag gates by the CLI). Other channels a tool might read
  selection from are not scanned.

### Claim invariants (enforced by tests + fuzz)

`tool = null ⇔ failing = null`; `fixed = null ⇔ new = null`; sorted, deduped;
`new ⊆ failing`; `fixed ∩ failing = ∅`; `exit 0 ⇒ failing = []` (when non-null);
`prev_exit 0 ⇒ fixed = []`; a baseline never carries `fixed`/`new`; every
identity is a substring of the (ANSI-stripped) output of the run it is
attributed to; determinism. The claim channel is independent of G1–G7: a
degraded line diff does not silence the adapter, which is exactly its payoff —
line 1 still names what was fixed and what broke when the body is a bounded
full view. The mechanized safety theorem (`TestExtract_neverFalseFixed`)
deletes every single line of the current output in turn and asserts a
still-failing identity is never claimed fixed; the skip/selection findings of
the adversarial review are each pinned by a regression test in
`internal/adapter/safety_test.go`. A new or changed parser must keep both
green.

## Deferred (see docs/non-goals.md)

Multi-tool composite outputs (one run printing both tsc and eslint shapes) are
refused by A2's exactly-one rule and deferred, as are additional capture eras
per tool.
