#!/bin/sh
# check-docs.sh — README hygiene guard. One robust, low-false-positive
# invariant that prevents the documented staleness trap: README.md never
# hardcodes a release version (v1.2.3 / 1.2.3) — docs link to Releases
# instead, so they never rot as versions advance. (Two-part versions like
# Go's 1.26 are allowed.)
# README.ja.md was retired 2026-08-02 (repo artifacts are English-only),
# so the former EN/JA sync guard is gone with it.
set -eu
cd "$(dirname "$0")/.."
fail=0

if hits=$(grep -nE '\bv?[0-9]+\.[0-9]+\.[0-9]+\b' README.md); then
  echo "✖ README.md hardcodes a release version (keep docs version-agnostic — link to Releases):"
  echo "$hits"
  fail=1
fi

[ "$fail" -eq 0 ] && echo "✓ docs guard passed"
exit "$fail"
