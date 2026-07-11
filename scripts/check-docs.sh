#!/bin/sh
# check-docs.sh — README hygiene guard (the EN/JA "sync guard"). Two robust,
# low-false-positive invariants that prevent the documented staleness trap:
#   1. Neither README hardcodes a release version (v1.2.3 / 1.2.3) — docs link to
#      Releases instead, so they never rot as versions advance. (Two-part
#      versions like Go's 1.26 are allowed.)
#   2. The two language variants stay mutually linked, so neither is orphaned.
# Deeper prose parity is left to review; these catch the staleness failure mode.
set -eu
cd "$(dirname "$0")/.."
fail=0

for f in README.md README.ja.md; do
  if [ ! -f "$f" ]; then
    echo "✖ missing $f"
    fail=1
    continue
  fi
  if hits=$(grep -nE '\bv?[0-9]+\.[0-9]+\.[0-9]+\b' "$f"); then
    echo "✖ $f hardcodes a release version (keep docs version-agnostic — link to Releases):"
    echo "$hits"
    fail=1
  fi
done

grep -q 'README\.ja\.md' README.md 2>/dev/null || { echo "✖ README.md must link to README.ja.md"; fail=1; }
grep -q 'README\.md' README.ja.md 2>/dev/null || { echo "✖ README.ja.md must link to README.md"; fail=1; }

[ "$fail" -eq 0 ] && echo "✓ docs guard passed"
exit "$fail"
