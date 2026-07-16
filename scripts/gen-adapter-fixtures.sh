#!/bin/sh
# gen-adapter-fixtures.sh — capture REAL tool transcripts for the adapter's
# golden fixtures (internal/adapter/testdata/captures/<tool>/<scenario>.out).
#
# Dev-only; CI never runs this. The committed goldens are reviewed bytes: this
# script only STAGES candidates into an output dir. Review each transcript,
# copy the scenarios worth pinning into testdata/captures/<tool>/ (rename with
# an era prefix like `v8-fail.out` when adding a second era of a tool), and
# merge the staged VERSIONS line — with provenance notes — into the committed
# VERSIONS. Never commit blind.
#
# Usage:
#   sh scripts/gen-adapter-fixtures.sh [-o OUTDIR] [tool[@version] ...]
#
#   tools:   go-test pytest jest vitest cargo-test tsc eslint  (default: all)
#   OUTDIR:  staging dir, relative to the repo root (default: tmp/adapter-fixtures)
#   @version pins an era for the npm tools (jest@29.7.0, vitest@1, tsc@5.5,
#            eslint@8.57.1 — installed into the throwaway fixture project) and
#            for pytest (pytest@7.4.4 — installed into a throwaway venv).
#            go-test and cargo-test always use the toolchain on PATH.
#
# With no tool arguments, tools whose toolchain is missing are skipped with a
# note; naming a tool explicitly makes its absence a hard error.
#
# Scenario matrix (per tool, where the tool can express it):
#   pass       everything green
#   fail       one file failing, another passing (the canonical fixed/new pair
#              partner of `pass`)
#   fail_ab    failures in two files
#   mixed      pass + fail + skip in one run (skip totals gate adapter claims)
#   filtered   name-level selection (-run/-k/-t) over a failing state — the
#              false-`fixed` trap the adapter must stay silent on
#   bail       stop at first failure (-x/--bail)
#   crash      the test process dies outside a normal assertion (panic,
#              import-time throw, collection error)
#   builderr   the tool fails before running anything (compile/syntax error)
#   quiet      reduced-output mode (-q/--quiet)
#   ansi       forced color over a failing run
#
# Machine paths are normalized exactly like the committed captures: the
# fixture project runs at a temp path that is rewritten to /home/dev/fixture
# (and $HOME to /home/dev) before staging, so transcripts never leak the
# capture machine. Timings, hashes, and ANSI bytes stay raw — they are part of
# the real output the adapter must survive.
set -eu
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=local

usage() {
  sed -n '2,44p' "$0" | sed 's/^# \{0,1\}//'
}

OUTDIR=tmp/adapter-fixtures
while getopts o:h opt; do
  case $opt in
    o) OUTDIR=$OPTARG ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))

EXPLICIT=$#
if [ $# -gt 0 ]; then
  TOOLS=$*
else
  TOOLS="go-test pytest jest vitest cargo-test tsc eslint"
fi

case $OUTDIR in /*) ;; *) OUTDIR=$PWD/$OUTDIR ;; esac

WORK=$(mktemp -d)
FIX=$WORK/fixture
trap 'rm -rf "$WORK"' EXIT INT TERM

GENERATED=""
SKIPPED=""

# --- plumbing -----------------------------------------------------------

# esc_sed PATH — escape a literal path for use in a `s|…|…|g` BRE.
esc_sed() {
  printf '%s' "$1" | sed 's/[.[\*^$]/\\&/g'
}

# normalize — rewrite capture-machine paths to the stable fixture identity.
# macOS mktemp dirs surface both as /var/folders/… and /private/var/folders/….
normalize() {
  sed \
    -e "s|$(esc_sed "/private$WORK")|/home/dev|g" \
    -e "s|$(esc_sed "$WORK")|/home/dev|g" \
    -e "s|$(esc_sed "$HOME")|/home/dev|g"
}

# capture TOOL SCENARIO CMD… — run CMD in the fixture project with combined
# non-TTY output (exactly what rundiff's runner feeds the adapter) and stage
# the normalized transcript + exit code.
capture() {
  _tool=$1 _scen=$2
  shift 2
  mkdir -p "$OUTDIR/$_tool"
  set +e
  (cd "$FIX" && "$@") >"$WORK/raw" 2>&1
  _code=$?
  set -e
  normalize <"$WORK/raw" >"$OUTDIR/$_tool/$_scen.out"
  printf '%s\n' "$_code" >"$OUTDIR/$_tool/$_scen.exit"
  printf '  %s/%s.out (exit %d)\n' "$_tool" "$_scen" "$_code"
  GENERATED="$GENERATED $_tool/$_scen"
}

# ver_line TOOL LINE — stage the provenance line for TOOL.
ver_line() {
  mkdir -p "$OUTDIR/$1"
  printf '%s\n' "$2" >"$OUTDIR/$1/VERSIONS"
}

# skip TOOL REASON — soft-skip in all-tools mode, hard error when named.
skip() {
  if [ "$EXPLICIT" -gt 0 ]; then
    echo "error: $1: $2" >&2
    exit 1
  fi
  echo "skip: $1 ($2)"
  SKIPPED="$SKIPPED $1"
}

fresh_fixture() {
  rm -rf "$FIX"
  mkdir -p "$FIX"
}

# node_project PKG[@VER] BIN — throwaway npm project with one tool installed;
# verifies node_modules/.bin/BIN exists. Returns nonzero (after a skip) if the
# install fails, e.g. offline.
node_project() {
  fresh_fixture
  printf '{ "name": "fixture", "private": true }\n' >"$FIX/package.json"
  if ! (cd "$FIX" && npm install --no-audit --no-fund --loglevel=error "$1" >/dev/null) ||
    ! [ -x "$FIX/node_modules/.bin/$2" ]; then
    skip "$2" "npm install $1 did not yield node_modules/.bin/$2"
    return 1
  fi
}

# --- go test ------------------------------------------------------------

write_go_project() { # $1 = value TestMul expects (6 passes, 7 fails)
  fresh_fixture
  mkdir -p "$FIX/calc" "$FIX/str"
  cat >"$FIX/go.mod" <<'EOF'
module example.com/fixture

go 1.24
EOF
  cat >"$FIX/calc/calc.go" <<'EOF'
package calc

func Add(a, b int) int { return a + b }

func Mul(a, b int) int { return a * b }
EOF
  cat >"$FIX/calc/calc_test.go" <<EOF
package calc

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Errorf("Add(2,3) = %d, want 5", got)
	}
}

func TestMul(t *testing.T) {
	if got := Mul(2, 3); got != $1 {
		t.Errorf("Mul(2,3) = %d, want $1", got)
	}
}
EOF
  cat >"$FIX/str/str.go" <<'EOF'
package str

import "strings"

func Upper(s string) string { return strings.ToUpper(s) }
EOF
  cat >"$FIX/str/str_test.go" <<'EOF'
package str

import "testing"

func TestUpper(t *testing.T) {
	if got := Upper("abc"); got != "ABC" {
		t.Errorf(`Upper("abc") = %q, want "ABC"`, got)
	}
}
EOF
}

gen_go_test() {
  if ! command -v go >/dev/null 2>&1; then
    skip go-test "go not on PATH"
    return 0
  fi
  if [ -n "$1" ]; then
    skip go-test "@version unsupported — put the wanted toolchain on PATH"
    return 0
  fi
  ver_line go-test "$(go version)"

  write_go_project 7
  capture go-test fail go test -count=1 ./...
  capture go-test verbose-fail go test -count=1 -v ./...
  # name-level selection over a failing state: every package reports `ok`
  # while TestMul is still broken — the adapter must not call this fixed.
  capture go-test filtered go test -count=1 -run TestAdd ./...

  cat >>"$FIX/str/str_test.go" <<'EOF'

func TestUpperEmpty(t *testing.T) {
	if got := Upper(""); got != "nonempty" {
		t.Errorf(`Upper("") = %q, want "nonempty"`, got)
	}
}
EOF
  capture go-test fail_ab go test -count=1 ./...

  write_go_project 7
  cat >>"$FIX/calc/calc_test.go" <<'EOF'

func TestCrash(t *testing.T) {
	panic("boom")
}
EOF
  capture go-test crash go test -count=1 ./...

  write_go_project 6
  echo "func Broken(" >>"$FIX/calc/calc.go"
  capture go-test builderr go test -count=1 ./...

  write_go_project 6
  capture go-test pass go test -count=1 ./...
  capture go-test verbose-pass go test -count=1 -v ./...
}

# --- pytest -------------------------------------------------------------

write_pytest_project() { # $1 = value test_mul expects (6 passes, 7 fails)
  fresh_fixture
  mkdir -p "$FIX/tests"
  cat >"$FIX/tests/test_math.py" <<EOF
def test_add():
    assert 2 + 3 == 5


def test_mul():
    assert 2 * 3 == $1


def test_zero():
    assert 0 + 0 == 0
EOF
  cat >"$FIX/tests/test_str.py" <<'EOF'
def test_upper():
    assert "abc".upper() == "ABC"
EOF
}

gen_pytest() {
  if [ -n "$1" ]; then
    if ! command -v python3 >/dev/null 2>&1; then
      skip pytest "python3 not on PATH"
      return 0
    fi
    if ! python3 -m venv "$WORK/pyvenv" ||
      ! "$WORK/pyvenv/bin/pip" install --quiet "pytest==$1"; then
      skip pytest "venv install of pytest==$1 failed"
      return 0
    fi
    PYTEST=$WORK/pyvenv/bin/pytest
  elif command -v pytest >/dev/null 2>&1; then
    PYTEST=$(command -v pytest)
  else
    skip pytest "pytest not on PATH"
    return 0
  fi
  ver_line pytest "$("$PYTEST" --version)"

  write_pytest_project 7
  capture pytest fail "$PYTEST"
  capture pytest bail "$PYTEST" -x
  capture pytest quiet "$PYTEST" -q
  capture pytest ansi "$PYTEST" --color=yes
  # -k selection over a failing state: the broken test_mul is deselected.
  capture pytest filtered "$PYTEST" -k add

  cat >>"$FIX/tests/test_str.py" <<'EOF'


def test_lower():
    assert "ABC".lower() == "abc!"
EOF
  capture pytest fail_ab "$PYTEST"

  write_pytest_project 7
  cat >>"$FIX/tests/test_math.py" <<'EOF'


import pytest


@pytest.mark.skip(reason="not ready")
def test_skipped():
    assert False
EOF
  capture pytest mixed "$PYTEST"

  write_pytest_project 6
  echo "def broken(" >>"$FIX/tests/test_math.py"
  capture pytest collecterr "$PYTEST"

  write_pytest_project 6
  capture pytest pass "$PYTEST"
}

# --- jest ---------------------------------------------------------------

write_jest_tests() { # $1 = value `multiplies` expects (6 passes, 7 fails)
  mkdir -p "$FIX/src" "$FIX/__tests__"
  cat >"$FIX/src/math.js" <<'EOF'
function add(a, b) { return a + b; }
function mul(a, b) { return a * b; }
module.exports = { add, mul };
EOF
  cat >"$FIX/__tests__/math.test.js" <<EOF
const { add, mul } = require('../src/math');
test('adds', () => { expect(add(2, 3)).toBe(5); });
test('multiplies', () => { expect(mul(2, 3)).toBe($1); });
EOF
  cat >"$FIX/__tests__/str.test.js" <<'EOF'
test('upper', () => { expect('abc'.toUpperCase()).toBe('ABC'); });
EOF
}

gen_jest() {
  if ! command -v npm >/dev/null 2>&1; then
    skip jest "npm not on PATH"
    return 0
  fi
  node_project "jest${1:+@$1}" jest || return 0
  JEST=$FIX/node_modules/.bin/jest
  ver_line jest "jest $("$JEST" --version)"

  write_jest_tests 7
  capture jest fail "$JEST"
  capture jest bail "$JEST" --bail
  capture jest ansi "$JEST" --colors
  # -t selection over a failing state: `multiplies` stays broken but skipped.
  capture jest filtered "$JEST" -t adds

  cat >>"$FIX/__tests__/str.test.js" <<'EOF'
test('lower', () => { expect('ABC'.toLowerCase()).toBe('abc!'); });
EOF
  capture jest fail_ab "$JEST"

  write_jest_tests 7
  cat >>"$FIX/__tests__/math.test.js" <<'EOF'
test.skip('skipped', () => { expect(1).toBe(2); });
EOF
  capture jest mixed "$JEST"

  write_jest_tests 6
  cat >"$FIX/__tests__/crash.test.js" <<'EOF'
throw new Error('boom at import time');
EOF
  capture jest crash "$JEST"
  rm "$FIX/__tests__/crash.test.js"

  capture jest pass "$JEST"
}

# --- vitest -------------------------------------------------------------

write_vitest_tests() { # $1 = value `multiplies` expects (6 passes, 7 fails)
  mkdir -p "$FIX/vtests"
  cat >"$FIX/vtests/math.test.js" <<EOF
import { expect, test } from 'vitest';
function add(a, b) { return a + b; }
function mul(a, b) { return a * b; }
test('adds', () => { expect(add(2, 3)).toBe(5); });
test('multiplies', () => { expect(mul(2, 3)).toBe($1); });
EOF
  cat >"$FIX/vtests/str.test.js" <<'EOF'
import { expect, test } from 'vitest';
test('upper', () => { expect('abc'.toUpperCase()).toBe('ABC'); });
EOF
}

gen_vitest() {
  if ! command -v npm >/dev/null 2>&1; then
    skip vitest "npm not on PATH"
    return 0
  fi
  node_project "vitest${1:+@$1}" vitest || return 0
  VITEST=$FIX/node_modules/.bin/vitest
  ver_line vitest "$("$VITEST" --version)"

  write_vitest_tests 7
  capture vitest fail "$VITEST" run
  capture vitest bail "$VITEST" run --bail 1
  # -t selection over a failing state: `multiplies` stays broken but skipped.
  capture vitest filtered "$VITEST" run -t adds

  cat >>"$FIX/vtests/str.test.js" <<'EOF'
test('lower', () => { expect('ABC'.toLowerCase()).toBe('abc!'); });
EOF
  capture vitest fail_ab "$VITEST" run

  write_vitest_tests 7
  cat >>"$FIX/vtests/math.test.js" <<'EOF'
test.skip('skipped', () => { expect(1).toBe(2); });
EOF
  capture vitest mixed "$VITEST" run

  write_vitest_tests 6
  cat >"$FIX/vtests/crash.test.js" <<'EOF'
throw new Error('boom at import time');
EOF
  capture vitest crash "$VITEST" run
  rm "$FIX/vtests/crash.test.js"

  capture vitest pass "$VITEST" run
}

# --- tsc ----------------------------------------------------------------

write_tsc_project() { # $1 = "good" | "bad" — overwrites in place, keeps node_modules
  mkdir -p "$FIX/ts"
  cat >"$FIX/ts/a.ts" <<'EOF'
export function add(a: number, b: number): number {
  return a + b;
}
EOF
  cat >"$FIX/ts/b.ts" <<'EOF'
export function upper(s: string): string {
  return s.toUpperCase();
}
EOF
  if [ "$1" = bad ]; then
    cat >>"$FIX/ts/a.ts" <<'EOF'

const n: number = 'six';
const s: string = 6;
EOF
    cat >>"$FIX/ts/b.ts" <<'EOF'

const t: string = 7;
EOF
  fi
}

gen_tsc() {
  if ! command -v npm >/dev/null 2>&1; then
    skip tsc "npm not on PATH"
    return 0
  fi
  node_project "typescript${1:+@$1}" tsc || return 0
  TSC=$FIX/node_modules/.bin/tsc
  ver_line tsc "tsc $("$TSC" --version)"

  write_tsc_project bad
  # keep the npm project out of the compile: name the files explicitly
  capture tsc fail "$TSC" --noEmit ts/a.ts ts/b.ts
  capture tsc pretty-fail "$TSC" --noEmit --pretty ts/a.ts ts/b.ts

  write_tsc_project good
  capture tsc pass "$TSC" --noEmit ts/a.ts ts/b.ts
}

# --- eslint -------------------------------------------------------------

write_eslint_project() { # $1 = "good" | "bad" — overwrites in place, keeps node_modules
  mkdir -p "$FIX/src"
  # both config eras side by side: eslint 9+ reads eslint.config.mjs and
  # ignores .eslintrc.json; eslint 8 does the reverse (without the flat flag).
  cat >"$FIX/eslint.config.mjs" <<'EOF'
export default [
  {
    rules: {
      'no-unused-vars': 'error',
      'no-undef': 'error',
      'prefer-const': 'warn',
    },
  },
];
EOF
  cat >"$FIX/.eslintrc.json" <<'EOF'
{
  "parserOptions": { "ecmaVersion": 2022, "sourceType": "module" },
  "rules": {
    "no-unused-vars": "error",
    "no-undef": "error",
    "prefer-const": "warn"
  }
}
EOF
  if [ "$1" = bad ]; then
    cat >"$FIX/src/bad.js" <<'EOF'
let unused = 1;
let never = missingGlobal;
export default never;
EOF
  else
    cat >"$FIX/src/bad.js" <<'EOF'
const fine = 1;
export default fine;
EOF
  fi
}

gen_eslint() {
  if ! command -v npm >/dev/null 2>&1; then
    skip eslint "npm not on PATH"
    return 0
  fi
  node_project "eslint${1:+@$1}" eslint || return 0
  ESLINT=$FIX/node_modules/.bin/eslint
  ver_line eslint "eslint $("$ESLINT" --version)"

  write_eslint_project bad
  capture eslint fail "$ESLINT" src
  capture eslint quiet "$ESLINT" --quiet src
  capture eslint ansi "$ESLINT" --color src

  write_eslint_project good
  capture eslint pass "$ESLINT" src
}

# --- cargo test ---------------------------------------------------------

write_cargo_project() { # $1 = value test_mul expects (6 passes, 7 fails)
  fresh_fixture
  mkdir -p "$FIX/src"
  cat >"$FIX/Cargo.toml" <<'EOF'
[package]
name = "fixture"
version = "0.1.0"
edition = "2021"
EOF
  cat >"$FIX/src/lib.rs" <<EOF
/// Adds two numbers.
///
/// \`\`\`
/// assert_eq!(fixture::add(2, 3), 5);
/// \`\`\`
pub fn add(a: i64, b: i64) -> i64 {
    a + b
}

pub fn mul(a: i64, b: i64) -> i64 {
    a * b
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_add() {
        assert_eq!(add(2, 3), 5);
    }

    #[test]
    fn test_mul() {
        assert_eq!(mul(2, 3), $1);
    }

    #[test]
    fn test_zero() {
        assert_eq!(add(0, 0), 0);
    }
}
EOF
}

gen_cargo_test() {
  if ! command -v cargo >/dev/null 2>&1; then
    skip cargo-test "cargo not on PATH"
    return 0
  fi
  if [ -n "$1" ]; then
    skip cargo-test "@version unsupported — put the wanted toolchain on PATH"
    return 0
  fi
  ver_line cargo-test "$(cargo --version) / $(rustc --version)"

  write_cargo_project 7
  capture cargo-test fail cargo test
  write_cargo_project 6
  capture cargo-test pass cargo test

  cat >>"$FIX/src/lib.rs" <<'EOF'

#[cfg(test)]
mod slow {
    #[test]
    #[ignore]
    fn test_slow() {
        assert!(true);
    }
}
EOF
  capture cargo-test ignored cargo test

  write_cargo_project 6
  cat >>"$FIX/src/lib.rs" <<'EOF'

/// Never true.
///
/// ```
/// assert_eq!(fixture::broken(), 1);
/// ```
pub fn broken() -> i64 {
    2
}
EOF
  capture cargo-test doctest cargo test

  # workspace: two member crates, the second failing.
  fresh_fixture
  mkdir -p "$FIX/alpha/src" "$FIX/beta/src"
  cat >"$FIX/Cargo.toml" <<'EOF'
[workspace]
members = ["alpha", "beta"]
resolver = "2"
EOF
  for _m in alpha beta; do
    cat >"$FIX/$_m/Cargo.toml" <<EOF
[package]
name = "$_m"
version = "0.1.0"
edition = "2021"
EOF
  done
  cat >"$FIX/alpha/src/lib.rs" <<'EOF'
pub fn add(a: i64, b: i64) -> i64 {
    a + b
}

#[cfg(test)]
mod tests {
    #[test]
    fn test_add() {
        assert_eq!(super::add(2, 3), 5);
    }
}
EOF
  cat >"$FIX/beta/src/lib.rs" <<'EOF'
pub fn mul(a: i64, b: i64) -> i64 {
    a * b
}

#[cfg(test)]
mod tests {
    #[test]
    fn test_mul() {
        assert_eq!(super::mul(2, 3), 7);
    }
}
EOF
  capture cargo-test workspace cargo test --workspace
}

# --- main ---------------------------------------------------------------

for _spec in $TOOLS; do
  _base=${_spec%%@*}
  _ver=${_spec#"$_base"}
  _ver=${_ver#@}
  echo "→ $_spec"
  case $_base in
    go-test) gen_go_test "$_ver" ;;
    pytest) gen_pytest "$_ver" ;;
    jest) gen_jest "$_ver" ;;
    vitest) gen_vitest "$_ver" ;;
    tsc) gen_tsc "$_ver" ;;
    eslint) gen_eslint "$_ver" ;;
    cargo-test) gen_cargo_test "$_ver" ;;
    *)
      echo "error: unknown tool '$_base' (want: go-test pytest jest vitest cargo-test tsc eslint)" >&2
      exit 2
      ;;
  esac
done

echo
if [ -n "$GENERATED" ]; then
  echo "staged into $OUTDIR:$(printf ' %s' $GENERATED)"
  echo "next: review each transcript, copy keepers into internal/adapter/testdata/captures/"
  echo "      (era prefix like v8-fail.out for a second era), merge VERSIONS with provenance."
fi
if [ -n "$SKIPPED" ]; then
  echo "skipped:$(printf ' %s' $SKIPPED)"
fi
[ -n "$GENERATED" ]
