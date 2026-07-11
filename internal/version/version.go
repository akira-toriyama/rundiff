// Package version carries rundiff's build identity. Version/Commit/Date are
// injected at release time via -ldflags "-X .../internal/version.Version=..."
// (see .goreleaser.yaml). For a plain `go build` / `go install` they stay empty
// and Get falls back to the module's embedded VCS stamps (runtime/debug), so a
// source build still reports a usable commit and date instead of a bare "dev".
package version

import (
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

// Get resolves the build identity, filling missing commit/date from the Go
// toolchain's embedded VCS metadata when the linker did not inject them.
func Get() Info {
	info := Info{Version: Version, Commit: Commit, Date: Date, Go: runtime.Version()}
	if info.Commit == "" || info.Date == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
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
		}
	}
	return info
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
