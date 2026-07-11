// Package version carries rundiff's build identity. Version/Commit/Date are
// injected at release time via -ldflags "-X .../internal/version.Version=..."
// (see .goreleaser.yaml). For a plain `go build` / `go install` they stay empty
// and Get falls back to the module's embedded VCS stamps (runtime/debug), so a
// source build still reports a usable commit and date instead of a bare "dev".
package version

import (
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
)

// Injected by the linker at release time. Do not set these here.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Info is the structured build identity, emitted by `rundiff version --json`.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
	Go      string `json:"go"`
}

// Get resolves the build identity, filling fields the linker left empty from the
// Go toolchain's embedded build metadata (see fillFromBuildInfo).
func Get() Info {
	info := Info{Version: Version, Commit: Commit, Date: Date, Go: runtime.Version()}
	if bi, ok := debug.ReadBuildInfo(); ok {
		info = fillFromBuildInfo(info, bi)
	}
	return info
}

// fillFromBuildInfo fills identity fields the linker did not inject from bi:
//   - commit/date from the VCS stamps (a `go build`/`go install` in a checkout);
//   - version from the module version (a `go install pkg@vX.Y.Z`, which carries
//     no VCS stamps but does record the installed module version) — so the
//     documented `go install …@latest` path reports its version, not a bare "dev".
//
// Injected (ldflags/release) values always win: each field is only filled when it
// is still empty ("dev" for version), so a release build's identity is authoritative.
// It is a pure function (bi injected, not read from the process) to stay testable.
func fillFromBuildInfo(info Info, bi *debug.BuildInfo) Info {
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Commit == "" {
				info.Commit = s.Value
			}
		case "vcs.time":
			if info.Date == "" {
				info.Date = s.Value
			}
		}
	}
	if info.Version == "dev" && isReleaseVersion(bi.Main.Version) {
		info.Version = bi.Main.Version
	}
	return info
}

// rePseudoVersion matches Go's module pseudo-version core — a UTC timestamp and
// commit prefix the toolchain synthesizes for an untagged build, e.g.
// v0.0.0-20260711172101-7751a999cac4 (optionally with a trailing "+dirty"). Not
// end-anchored so the dirty-tree suffix is covered; a real semver tag never
// contains this token, so matching anywhere is safe.
var rePseudoVersion = regexp.MustCompile(`-\d{14}-[0-9a-f]{12}`)

// isReleaseVersion reports whether v is a real tagged module version worth
// surfacing (a clean `go install pkg@vX.Y.Z`). It rejects the empty/"(devel)"
// placeholders and pseudo-versions: for a plain checkout `go build` (whose main
// module Go now stamps with a pseudo-version) the identity stays the cleaner
// "dev (commit, date)" rather than a redundant timestamp+commit string.
func isReleaseVersion(v string) bool {
	return v != "" && v != "(devel)" && !rePseudoVersion.MatchString(v)
}

// Human renders the build identity as a single line for `--version` and the
// human form of `rundiff version`.
func (i Info) Human() string {
	var b strings.Builder
	b.WriteString(i.Version)
	if i.Commit != "" {
		c := i.Commit
		if len(c) > 12 {
			c = c[:12]
		}
		b.WriteString(" (" + c)
		if i.Date != "" {
			b.WriteString(", " + i.Date)
		}
		b.WriteString(")")
	} else if i.Date != "" {
		b.WriteString(" (" + i.Date + ")")
	}
	b.WriteString(" " + i.Go)
	return b.String()
}
