#!/bin/sh
# check.sh — the full local verification, runnable by you or by Claude Code with
# no TTY. Mirrors what CI enforces (build.yml → shared go-ci reusable: module
# hygiene / build / vet / race-test / lint; plus docs guard, smoke, govulncheck),
# so a green run here means a green CI.
set -eu
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=local

echo "→ module hygiene (go mod tidy -diff + verify)"
go mod tidy -diff
go mod verify

echo "→ go build"
go build ./...

echo "→ go vet"
go vet ./...

echo "→ go test -race (+ coverage)"
go test -race -covermode=atomic -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | tail -1

echo "→ docs guard (README version-agnostic + EN/JA cross-link)"
sh scripts/check-docs.sh

if command -v golangci-lint >/dev/null 2>&1; then
  echo "→ golangci-lint"
  golangci-lint run ./...
else
  echo "→ golangci-lint (skipped — not installed; CI runs it)"
fi

if command -v govulncheck >/dev/null 2>&1; then
  echo "→ govulncheck"
  govulncheck ./...
else
  echo "→ govulncheck (skipped — not installed; CI runs it)"
fi

echo "→ build binary for live checks"
go build -o bin/rundiff ./cmd/rundiff
BIN="$(pwd)/bin/rundiff"

echo "→ smoke: version / baseline+rerun / a changed line surfaces"
RUNDIFF_CACHE_DIR="$(mktemp -d)"
export RUNDIFF_CACHE_DIR
D="$(mktemp -d)"; F="$D/out"
"$BIN" version
# The cache key is argv+cwd+branch, so smoke a FIXED argv (`cat $F`) whose file
# content changes between runs — otherwise a new argv is just a new baseline.
printf 'a\nb\nc\n' > "$F"
# first run establishes the baseline (transition=baseline)
"$BIN" --json -- cat "$F" | grep -q '"transition":"baseline"'
# an identical re-run is fully unchanged (transition=still_passing, added=0)
"$BIN" --json -- cat "$F" | grep -q '"transition":"still_passing"'
"$BIN" --json -- cat "$F" | grep -q '"added":0'
# a changed line surfaces as one added (counts stay real even when degraded)
printf 'a\nX\nc\n' > "$F"
"$BIN" --json -- cat "$F" | grep -q '"added":1'

echo "→ smoke: hook rewrite (the protocol, end to end) / hook print"
EV='{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./...","timeout":600000}}'
# a target command is rewritten to DIRECT argv (never a shell string — a shell
# blinds the adapter's whole-token gates, see docs/algorithm.md A7)
printf '%s' "$EV" | "$BIN" hook rewrite | grep -q '"command":"rundiff -- go test \./\.\.\."'
# the whole tool_input is echoed back: a dropped timeout would silently shorten a run
printf '%s' "$EV" | "$BIN" hook rewrite | grep -q '"timeout":600000'
# it never approves anything on the user's behalf
! printf '%s' "$EV" | "$BIN" hook rewrite | grep -q 'permissionDecision'
# a metacharacter ⇒ no decision: zero bytes, exit 0
PIPED='{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./... | tee log"}}'
test -z "$(printf '%s' "$PIPED" | "$BIN" hook rewrite)"
# garbage in ⇒ still zero bytes, still exit 0 (it runs before EVERY Bash call)
test -z "$(echo 'not json' | "$BIN" hook rewrite)"
# the snippet carries the missing-binary guard and never blanket-approves rundiff
"$BIN" hook print --json | grep -q 'command -v rundiff'
! "$BIN" hook print --json | grep -q 'Bash(rundiff:\*)'
"$BIN" hook print --json | python3 -c 'import json,sys; json.load(sys.stdin)'

rm -rf "$RUNDIFF_CACHE_DIR" "$D"
echo "✓ all checks passed"
