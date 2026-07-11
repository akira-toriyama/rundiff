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

## Deferred (candidates for a later version)

- **File-level `fixed` / `new` adapters.** The line-level `added_lines` /
  `removed_lines` are generic. An adapter layer (jest / vitest / pytest / cargo /
  go test / tsc / eslint) would post-process those into `fixed: ["src/x.test.ts"]`
  / `new: [...]` file arrays. The JSON schema reserves those top-level keys; the
  core never emits them under `v:1`.
- **`--baseline <id>` history.** Today rundiff keeps one baseline per key (the
  last run). Pinning an older comparison point needs a per-key run history.
- **A Claude Code `PreToolUse` hook** that auto-wraps target commands, so an
  agent gets the delta without remembering to prefix `rundiff --`.
- **CLI flags for the per-rule escapes and opt-in normalization**
  (`--no-time`, `--normalize-ptr`, …). They exist in the core `Options`; only a
  curated subset (`--json`, `--raw`, `--full`, `--churn`) is wired to the CLI.
- **A man page.** `rundiff --help` (cobra `Example:` blocks) and
  `rundiff completion <shell>` cover the ergonomics without pulling a
  markdown→roff dependency into the supply chain.
