package adapter

import (
	"path"
	"strings"
)

// argv helpers shared by the per-tool hint/blockedFlags implementations.
//
// Hints scan the basename of EVERY argv token, which subsumes one-level
// launcher unwrapping (npx jest, pnpm exec vitest, node_modules/.bin/tsc,
// python -m pytest all expose the tool as some token's basename). Over-firing
// is harmless — a hint only narrows the candidate set, the output fingerprint
// decides — while under-firing (npm test, make check) leaves every parser a
// candidate.

// hasBase reports whether any argv token's path basename equals one of names.
func hasBase(argv []string, names ...string) bool {
	for _, a := range argv {
		b := path.Base(a)
		for _, n := range names {
			if b == n {
				return true
			}
		}
	}
	return false
}

// hasWord reports whether any argv token equals one of names verbatim
// (subcommands like "test" — no basename games).
func hasWord(argv []string, names ...string) bool {
	for _, a := range argv {
		for _, n := range names {
			if a == n {
				return true
			}
		}
	}
	return false
}

// hasFlag reports whether any argv token is one of flags, either exactly or in
// its --flag=value form. Over-matching a token that happens to look like a
// blocked flag is conservative (silence), never a false claim.
func hasFlag(argv []string, flags ...string) bool {
	for _, a := range argv {
		for _, f := range flags {
			if a == f || strings.HasPrefix(a, f+"=") {
				return true
			}
		}
	}
	return false
}
