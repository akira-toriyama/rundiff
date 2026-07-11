package delta

import (
	"regexp"
	"strings"
)

// Normalization turns each output line into a *match key* that cancels the
// run-to-run noise a re-run produces (timestamps, elapsed times, temp paths,
// ANSI color, goroutine ids…) so that only real changes survive the diff. The
// key is never displayed — the diff always shows the raw line it stood for
// (the verbatim-subset invariant). Rules are ordered and each is idempotent,
// total (never errors on arbitrary bytes), and single-line (introduces no \n).
//
// The guiding rule is safety: normalization that is too aggressive HIDES a real
// change, which is worse than leaving noise in. So anything that could plausibly
// be asserted data (bare integers, bare dates, 0x… values, git shas) is left
// alone by default and gated behind an opt-in.

// ANSI / ESC stripping (Stage 1). Interpreted string literals so \x1b is the ESC
// byte. A5 (lone ESC) runs last via strings.ReplaceAll so it can't eat the final
// byte of A1–A4.
var (
	reCSI    = regexp.MustCompile("\x1b\\[[0-9:;<=>?]*[ -/]*[@-~]")        // A1 color/cursor
	reOSC    = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)?") // A2 title/hyperlink
	reStrEsc = regexp.MustCompile("\x1b[PX^_][^\x1b]*(?:\x1b\\\\|\x07)?")  // A3 DCS/PM/APC/SOS
	reNF     = regexp.MustCompile("\x1b[ -/]*[0-~]")                       // A4 charset/nF escapes
)

// Timestamps (Stage 3), durations (Stage 4), temp paths (Stage 5), identifiers
// (Stage 6), opt-in (Stage 7). Backtick literals keep the regex readable.
var (
	reISOTS = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d{1,9})?(?:Z|[+-]\d{2}:?\d{2})?`) // T1
	reClock = regexp.MustCompile(`\b\d{1,2}:\d{2}:\d{2}(?:[.,]\d{1,9})?\b`)                                       // T2 (seconds required)

	reDurCompound = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:ns|µs|us|ms|h|m|s)(?:\d+(?:\.\d+)?(?:ns|µs|us|ms|h|m|s))+\b`)                       // D1
	reDurAbbrev   = regexp.MustCompile(`\b\d+(?:\.\d+)?\s?(?:ns|µs|us|ms)\b|\b\d+\.\d+\s?s\b`)                                                // D2
	reDurKeyword  = regexp.MustCompile(`\b(in|took|elapsed|after|ran|finished|completed)\s+\d+(?:\.\d+)?\s?(?:h|m|s|ms|sec|secs|min|mins)\b`) // D3
	// D4 (unanchored spelled-out durations like "5 minutes") was dropped: it
	// masked ambiguous tokens ("5 min" = minimum) and could hide a real change.
	// Elapsed-time noise is covered by D1 (compound), D2 (abbrev/dec-sec) and D3
	// (keyword-anchored). The keyword makes a duration overwhelmingly likely in
	// build/test output — rundiff's domain — but is not a proof: a bare m/s after
	// in/after/ran could be distance ("target in 5m" = 5 metres). --no-dur is the
	// escape when a keyword+unit is actually asserted data.

	// The temp-path tail class stops at delimiters that commonly glue run-varying
	// data to a path (comma, =, ], }, (, >, &, |, quotes) so the mask never
	// swallows a value past the path boundary and hides it.
	reTmpMac      = regexp.MustCompile("(?:/private)?/var/folders/[^\\s:)\\]}(>,=\"'&|]*")                                                             // P1
	reTmpLinux    = regexp.MustCompile("(?:/private)?/tmp/[^\\s:)\\]}(>,=\"'&|]*")                                                                     // P2
	reTmpBasename = regexp.MustCompile("\\btmp[._-][A-Za-z0-9]{6,}\\b|\\b[0-9a-f]{6,}\\.tmp\\b|\\bpytest-of-[^\\s:)\\]}(>,=\"'&|]*|\\bpytest-\\d+\\b") // P3

	reGoroutine = regexp.MustCompile(`goroutine \d+`)                                                                   // I1
	reUUID      = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`) // I2
	// No leading \b: it is a non-word boundary before the `[::1]` arm and would
	// make that arm dead. The host tokens are specific enough on their own.
	rePort = regexp.MustCompile(`((?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])):\d+`) // I3

	reAddr   = regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`)    // X1 opt-in
	reHex    = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)    // X2 opt-in
	reDate   = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`) // X3 opt-in
	reSpaces = regexp.MustCompile(`\s+`)                   // X4 opt-in
)

// normalizer applies the active ruleset for one Diff call. The booleans are read
// from Options once and captured so the per-line hot path is branch-cheap.
type normalizer struct {
	raw      bool // --raw: identity (callers skip normalization entirely)
	noTime   bool
	noDur    bool
	noTmp    bool
	noUUID   bool
	noPort   bool
	ptr      bool
	hex      bool
	date     bool
	collapse bool
}

func newNormalizer(opt Options) normalizer {
	return normalizer{
		raw:      opt.Raw,
		noTime:   opt.NoTime,
		noDur:    opt.NoDur,
		noTmp:    opt.NoTmp,
		noUUID:   opt.NoUUID,
		noPort:   opt.NoPort,
		ptr:      opt.NormalizePtr,
		hex:      opt.NormalizeHex,
		date:     opt.NormalizeDate,
		collapse: opt.CollapseSpaces,
	}
}

// moreAggressive returns a copy with the opt-in rules (0x/hex/date/collapse)
// forced ON, and false when that would change nothing — under --raw (nothing is
// normalized) or when every opt-in is already on. It is used only by the
// normalization_uncertain probe (degrade.go): the caller's default-OFF escapes
// (noTime, noDur, …) are left untouched so an escape the caller set deliberately
// is not second-guessed. The probe's output is never displayed — only its delta
// SIZE feeds the degrade decision — so turning on these (individually unsafe)
// rules here can never hide a real change.
func (n normalizer) moreAggressive() (normalizer, bool) {
	if n.raw || (n.ptr && n.hex && n.date && n.collapse) {
		return n, false
	}
	n.ptr, n.hex, n.date, n.collapse = true, true, true, true
	return n, true
}

// line normalizes a single split line (no trailing \n) into its match key.
func (n normalizer) line(s string) string {
	if n.raw {
		return s
	}

	// Stage 0 — carriage returns. Trim one trailing \r (CRLF), then collapse
	// interior progress frames by keeping the segment after the last \r.
	s = strings.TrimSuffix(s, "\r")
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		s = s[i+1:]
	}

	// Stage 1 — ANSI / ESC.
	s = reCSI.ReplaceAllString(s, "")
	s = reOSC.ReplaceAllString(s, "")
	s = reStrEsc.ReplaceAllString(s, "")
	s = reNF.ReplaceAllString(s, "")
	if strings.IndexByte(s, '\x1b') >= 0 {
		s = strings.ReplaceAll(s, "\x1b", "") // A5 lone ESC
	}

	// Stage 3 — timestamps (T1 before T2). NoTime disables the entire timestamp
	// stage, so a caller can un-mask an ISO datetime that is asserted data — not
	// just the HH:MM:SS clock. (reISOTS must stay gated with reClock: leaving it
	// unconditional would hide a real change to an ISO datetime under --no-time,
	// the exact cardinal-rule violation the escape exists to avoid.)
	if !n.noTime {
		s = reISOTS.ReplaceAllString(s, "<TS>")
		s = reClock.ReplaceAllString(s, "<TIME>")
	}

	// Stage 4 — durations (compound, abbrev/dec-sec, keyword-anchored).
	if !n.noDur {
		s = reDurCompound.ReplaceAllString(s, "<DUR>")
		s = reDurAbbrev.ReplaceAllString(s, "<DUR>")
		s = reDurKeyword.ReplaceAllString(s, "${1} <DUR>")
	}

	// Stage 5 — temp paths.
	if !n.noTmp {
		s = reTmpMac.ReplaceAllString(s, "<TMP>")
		s = reTmpLinux.ReplaceAllString(s, "<TMP>")
		s = reTmpBasename.ReplaceAllString(s, "<TMP>")
	}

	// Stage 6 — high-confidence identifiers.
	s = reGoroutine.ReplaceAllString(s, "goroutine <N>")
	if !n.noUUID {
		s = reUUID.ReplaceAllString(s, "<UUID>")
	}
	if !n.noPort {
		s = rePort.ReplaceAllString(s, "${1}:<PORT>")
	}

	// Stage 7 — opt-in (default off; each can hide a real change).
	if n.ptr {
		s = reAddr.ReplaceAllString(s, "<ADDR>")
	}
	if n.hex {
		s = reHex.ReplaceAllString(s, "<HEX>")
	}
	if n.date {
		s = reDate.ReplaceAllString(s, "<DATE>")
	}
	if n.collapse {
		s = reSpaces.ReplaceAllString(s, " ")
	}

	// Stage 8 — trailing whitespace left by ANSI/CR removal.
	return strings.TrimRight(s, " \t")
}
