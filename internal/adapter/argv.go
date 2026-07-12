package adapter

import (
	"path"
	"strings"
)

// argv analysis shared by the per-tool hint/blockedFlags/selectionFlags impls.
//
// resolveTool identifies the wrapped tool from argv's COMMAND POSITION,
// unwrapping exactly one launcher level, and returns two things:
//
//   - bases: the basenames a hint may match (argv[0] plus, if a launcher was
//     unwrapped, the launched tool). A tool name appearing in a launcher FLAG
//     VALUE (npx -p tsc …), a script name (npm run tsc), or a script argument
//     (python runner.py -m pytest) is NOT the command position and never
//     becomes a base — otherwise it would narrow the candidate set away from a
//     composite the exactly-one rule must refuse.
//   - toolArgs: the tool's OWN argument tokens (everything after the tool
//     token). The launcher's flags are excluded, so a gate scanning toolArgs
//     never mistakes `python -m`'s -m for pytest's marker flag.
func resolveTool(argv []string) (bases, toolArgs []string) {
	if len(argv) == 0 {
		return nil, nil
	}
	cmd := path.Base(argv[0])
	bases = []string{cmd}
	rest := argv[1:]
	switch cmd {
	case "npx", "pnpx", "bunx":
		return unwrapExec(bases, rest)
	case "npm", "pnpm", "yarn", "bun":
		if len(rest) > 0 && (rest[0] == "exec" || rest[0] == "dlx") {
			return unwrapExec(bases, rest[1:])
		}
		return bases, rest // bare `npm test` — npm is the script runner; no unwrap
	case "python", "python3":
		return unwrapPython(bases, rest)
	default:
		return bases, rest
	}
}

// execValueFlags are the launcher flags (npx / pnpm exec / …) that consume a
// following token as their value — that value is NOT the launched tool.
var execValueFlags = map[string]bool{
	"-p": true, "--package": true, "-c": true, "--call": true,
	"-w": true, "--workspace": true,
}

func unwrapExec(bases, rest []string) ([]string, []string) {
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if strings.HasPrefix(a, "-") {
			if execValueFlags[a] {
				i++ // skip the flag's value (glued -p=… stays here, harmless)
			}
			continue
		}
		bases = append(bases, path.Base(a))
		return bases, rest[i+1:]
	}
	return bases, nil
}

func unwrapPython(bases, rest []string) ([]string, []string) {
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if a == "-m" && i+1 < len(rest) {
			bases = append(bases, path.Base(rest[i+1]))
			return bases, rest[i+2:]
		}
		if a == "-c" {
			return bases, nil // python -c '<code>': not a tool we recognize
		}
		if strings.HasPrefix(a, "-") {
			continue // interpreter flag (-B, -O, …)
		}
		// A non-flag before -m is a script path (python runner.py …): the
		// script is the command, its own -m/args are not the interpreter's.
		bases = append(bases, path.Base(a))
		return bases, rest[i+1:]
	}
	return bases, nil
}

// invokes reports whether argv's command position (launcher-unwrapped) names
// one of the given tools.
func invokes(argv []string, names ...string) bool {
	bases, _ := resolveTool(argv)
	for _, b := range bases {
		for _, n := range names {
			if b == n {
				return true
			}
		}
	}
	return false
}

// hasWord reports whether any token equals one of names verbatim (subcommands
// like "test" — no basename games).
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
// flag=value form. Callers list every spelling the tool accepts (Go's flag
// package takes one OR two dashes, and every `go test` flag also has a
// `-test.`-prefixed form). Over-matching is conservative (silence), never a lie.
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

// hasFlagPrefix reports a token starting with the given prefix (-Z…).
func hasFlagPrefix(argv []string, prefix string) bool {
	for _, a := range argv {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}
