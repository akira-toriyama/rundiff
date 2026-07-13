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

// pythonValueFlags are interpreter flags that consume a SEPARATE-token value —
// that value is not the tool/script and must be skipped, not read as one.
var pythonValueFlags = map[string]bool{"-W": true, "-X": true, "--check-hash-based-pycs": true}

func unwrapPython(bases, rest []string) ([]string, []string) {
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "-m" && i+1 < len(rest):
			bases = append(bases, path.Base(rest[i+1]))
			return bases, rest[i+2:]
		case strings.HasPrefix(a, "-m") && len(a) > 2:
			bases = append(bases, path.Base(a[2:])) // glued -mMODULE
			return bases, rest[i+1:]
		case a == "-c" || (strings.HasPrefix(a, "-c") && len(a) > 2):
			return bases, nil // python -c '<code>': opaque (see commandOpaque)
		case pythonValueFlags[a]:
			i++ // skip the flag's separate-token value (-W error, -X dev)
		case strings.HasPrefix(a, "-"):
			continue // valueless/glued interpreter flag (-B, -O, -Werror)
		default:
			// A non-flag before -m is a script path (python runner.py …): the
			// script is the command, its own -m/args are not the interpreter's.
			bases = append(bases, path.Base(a))
			return bases, rest[i+1:]
		}
	}
	return bases, nil
}

// shells run a command the gates cannot read. `bash -lc 'go test -run X'` puts
// the WHOLE command in one token, so hasFlag — which compares whole tokens —
// sees `-lc` and a blob; `bash run.sh` hides it in a file. Neither the flag
// spelling nor the presence of -c can be relied on (a login shell's -lc is one
// glued token), so the command position alone decides.
var opaqueShells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true, "ksh": true, "ash": true,
}

// argvCarriers exec another program from their own argv. Two ways they blind a
// gate: they can carry an ENV ASSIGNMENT as a plain token (`env
// GOFLAGS=-run=X go test` — hasFlag sees the single token "GOFLAGS=-run=X",
// and the CLI lifts GOFLAGS from its OWN environment, where it is not set), and
// they displace the tool from the command position so no hint fires. Cheap to
// list, and the alternative — trusting each parser's per-identity evidence to
// catch it — is exactly the reasoning a false `fixed` punishes.
var argvCarriers = map[string]bool{
	"env": true, "direnv": true, "dotenv": true, "xargs": true, "time": true,
	"timeout": true, "nice": true, "stdbuf": true, "nohup": true, "sudo": true,
}

// commandOpaque reports whether argv runs a command rundiff cannot introspect:
// a shell or an argv-carrying launcher in the command position, `npx -c/--call
// '<shell>'`, `npm|pnpm|yarn|bun exec -c '<shell>'`, or `python -c '<code>'`.
// A tool's own selection flag can hide inside that string (npx -c 'vitest -t
// /x/', env GOFLAGS=-run=X), invisible to the gates, so a cross-run claim must
// abstain — the failing set (from printed lines) stays sound, only the
// fixed/new pair is withheld.
func commandOpaque(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	cmd := path.Base(argv[0])
	if opaqueShells[cmd] || argvCarriers[cmd] {
		return true
	}
	switch cmd {
	case "npx", "pnpx", "bunx":
		return hasFlag(argv, "-c", "--call")
	case "npm", "pnpm", "yarn", "bun":
		return len(argv) > 1 && (argv[1] == "exec" || argv[1] == "dlx") && hasFlag(argv, "-c", "--call")
	case "python", "python3":
		for _, a := range argv[1:] {
			switch {
			case a == "-m" || (strings.HasPrefix(a, "-m") && len(a) > 2):
				return false // reached the module: any later -c is its config flag, not python's
			case a == "-c" || (strings.HasPrefix(a, "-c") && len(a) > 2):
				return true
			case !strings.HasPrefix(a, "-"):
				return false // a script path: later -c is the script's
			}
		}
	}
	return false
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
