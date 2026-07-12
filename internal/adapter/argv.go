package adapter

import (
	"path"
	"strings"
)

// argv helpers shared by the per-tool hint/blockedFlags implementations.
//
// Hints must look only at tokens that can actually BE the tool: the command
// position, plus one unambiguous launcher level. Scanning every token is not
// safe — an npm SCRIPT named "tsc" (argv: npm run tsc) or a path argument
// containing a tool name would narrow the candidate set away from a composite
// ambiguity the exactly-one rule exists to refuse. Under-firing is fine: with
// no hint every parser is a candidate and the output fingerprint decides.

// commandBases returns the basenames of the command-position tokens: argv[0],
// plus the launched tool for npx/pnpx/bunx, `npm|pnpm|yarn|bun exec|dlx`, and
// `python -m <tool>`. Bare `pnpm <name>` / `yarn <name>` are NOT unwrapped —
// <name> may be a script whose name collides with a tool.
func commandBases(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	bases := []string{path.Base(argv[0])}
	rest := argv[1:]
	switch bases[0] {
	case "npx", "pnpx", "bunx":
		// the next non-flag token is the tool
	case "npm", "pnpm", "yarn", "bun":
		if len(rest) == 0 || (rest[0] != "exec" && rest[0] != "dlx") {
			return bases
		}
		rest = rest[1:]
	case "python", "python3":
		for i, a := range rest {
			if a == "-m" && i+1 < len(rest) {
				bases = append(bases, rest[i+1])
				break
			}
		}
		return bases
	default:
		return bases
	}
	for _, a := range rest {
		if strings.HasPrefix(a, "-") {
			continue
		}
		bases = append(bases, path.Base(a))
		break
	}
	return bases
}

// invokes reports whether argv's command position (launcher-unwrapped) names
// one of the given tools.
func invokes(argv []string, names ...string) bool {
	for _, b := range commandBases(argv) {
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

// hasFlag reports whether any token is one of flags, either exactly or in its
// flag=value form. Callers list both the -flag and --flag spellings where the
// tool accepts both (Go's flag package does). Over-matching a token that
// merely looks like a blocked flag is conservative (silence), never a false
// claim.
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

// hasFlagPrefix reports an argv token starting with the given prefix (-Z…, -k…).
func hasFlagPrefix(argv []string, prefix string) bool {
	for _, a := range argv {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}
