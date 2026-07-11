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

## Deferred (see docs/non-goals.md)

The file-level `fixed`/`new` adapter layer is planned but not in this version; the
JSON schema reserves room for it.
