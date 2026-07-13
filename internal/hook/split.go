package hook

import "strings"

// maxCommandBytes caps what we are willing to reason about. A command an agent
// actually typed is tens of bytes; a multi-kilobyte one is a generated blob, and
// a rewriter's confidence in a blob it did not read is exactly the thing this
// package refuses to have.
const maxCommandBytes = 4096

// safeByte is the whole safety argument in one function. Each permitted byte is
// INERT in POSIX-sh word context — it cannot quote, split, expand, redirect,
// glob, comment, background or terminate. Everything else is out, so this set is
// what makes the rewrite safe WITHOUT a shell quoter (see the package doc's
// money property).
//
// The interesting members are the ones that look risky and are not:
//
//	=  only special as a LEADING word (an assignment). A rewritten command's
//	   leading word is always the rundiff binary, and an original whose argv[0]
//	   carries `=` is refused as an env prefix — so every surviving `=` sits in
//	   an argument, where sh treats it as an ordinary character (-run=TestX).
//	:  @  +  ,  ordinary characters to sh; they carry meaning only to the tools
//	   (--ext .ts,.tsx, a go module path, a cargo feature).
//	-  .  /  _  the alphabet of flags and paths, and nothing else.
//
// And the ones that look harmless and are not, hence excluded: `~` (tilde
// expansion), `%` and `^` (harmless in sh, but not in every shell an agent might
// be configured with), `!` (history expansion), `#` (comment), `*` `?` `[` `]`
// (globs), `{` `}` (brace expansion), and every byte >= 0x80 — a non-ASCII path
// is perfectly legal and perfectly fine to run, but it is not something we can
// re-emit unquoted with a straight face, so it is silence, not a gamble.
func safeByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	}
	switch b {
	case '_', '-', '.', '/', '=', ':', '@', '+', ',':
		return true
	}
	return false
}

// separator is the only whitespace we accept; it is also the only whitespace
// that sh's default IFS splits words on (besides '\n', which we refuse outright
// because it terminates a command).
func separator(b byte) bool { return b == ' ' || b == '\t' }

// Split is a SAFETY GATE that happens to return words — not a convenience
// tokenizer. It gates EVERY BYTE of the whole string BEFORE splitting, and the
// order matters:
//
// Splitting first and gating the fields would be a silent divergence from the
// shell. strings.Fields splits on unicode.IsSpace — which includes '\v', '\f'
// and U+00A0 — and a real shell does NOT. So `go test\vfoo` split-first yields
// two clean-looking fields that our join would re-emit as two words, while sh
// would have passed `test\vfoo` as ONE argument. Gate-first makes that
// impossible: '\v' is not a safe byte and not a separator, so the string is
// refused before it can be misread. (After the gate, the only whitespace left is
// ' ' and '\t', where Fields and sh agree exactly — which is why Fields is safe
// to use on the SECOND line and not the first.)
//
// ok=false is the answer for everything else: any quote, any metacharacter, any
// newline or NUL, any byte >= 0x80, anything over maxCommandBytes, and the empty
// command. There is no partial success and no repair — an unsplittable command
// is simply not one this package has an opinion about.
func Split(s string) (argv []string, ok bool) {
	if len(s) > maxCommandBytes {
		return nil, false
	}
	for i := 0; i < len(s); i++ {
		if b := s[i]; !safeByte(b) && !separator(b) {
			return nil, false
		}
	}
	argv = strings.Fields(s)
	if len(argv) == 0 {
		return nil, false
	}
	return argv, true
}
