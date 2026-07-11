package version

import (
	"runtime/debug"
	"testing"
)

func TestHuman_shapes(t *testing.T) {
	// Human() is the --version / `rundiff version` line; assert its four shapes
	// and the 12-char commit truncation exactly (each regresses silently otherwise).
	cases := []struct {
		name string
		in   Info
		want string
	}{
		{"commit+date, 12-char trunc", Info{Version: "1.2.3", Commit: "abcdef1234567890", Date: "2026-01-01", Go: "go1.26"}, "1.2.3 (abcdef123456, 2026-01-01) go1.26"},
		{"commit only", Info{Version: "dev", Commit: "short", Go: "go1.26"}, "dev (short) go1.26"},
		{"date only", Info{Version: "dev", Date: "2026-01-01", Go: "go1.26"}, "dev (2026-01-01) go1.26"},
		{"neither", Info{Version: "dev", Go: "go1.26"}, "dev go1.26"},
	}
	for _, c := range cases {
		if got := c.in.Human(); got != c.want {
			t.Errorf("%s: Human() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestGet_alwaysPopulatesVersionAndGo(t *testing.T) {
	// Whatever the build mode, Version and Go are never empty (Get seeds them).
	got := Get()
	if got.Version == "" {
		t.Error("Get().Version is empty")
	}
	if got.Go == "" {
		t.Error("Get().Go is empty")
	}
}

func TestFillFromBuildInfo_moduleVersion(t *testing.T) {
	// `go install pkg@vX.Y.Z`: no VCS stamps, but Main.Version carries the
	// installed module version — it must fill the still-"dev" version so the
	// documented go-install path does not report a bare "dev".
	got := fillFromBuildInfo(Info{Version: "dev"}, &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}})
	if got.Version != "v1.2.3" {
		t.Errorf("Version = %q, want v1.2.3 (module version)", got.Version)
	}
}

func TestFillFromBuildInfo_pseudoVersionStaysDev(t *testing.T) {
	// A plain checkout `go build` stamps the main module with a pseudo-version;
	// surfacing it would be a redundant timestamp+commit (the commit is already in
	// the parens), so the identity stays "dev".
	for _, v := range []string{
		"v0.0.0-20260711172101-7751a999cac4",       // clean pseudo-version
		"v0.0.0-20260711172101-7751a999cac4+dirty", // dirty-tree pseudo-version
	} {
		got := fillFromBuildInfo(Info{Version: "dev"}, &debug.BuildInfo{Main: debug.Module{Version: v}})
		if got.Version != "dev" {
			t.Errorf("Main.Version %q: got %q, want dev (a pseudo-version must not surface)", v, got.Version)
		}
	}
}

func TestFillFromBuildInfo_develPlaceholderStaysDev(t *testing.T) {
	// "(devel)" is the toolchain placeholder for a non-versioned build; it must
	// not surface — the identity stays "dev".
	got := fillFromBuildInfo(Info{Version: "dev"}, &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}})
	if got.Version != "dev" {
		t.Errorf("Version = %q, want dev ((devel) must not surface)", got.Version)
	}
}

func TestFillFromBuildInfo_vcsStamps(t *testing.T) {
	// A checkout `go build`: commit/date come from the VCS stamps.
	got := fillFromBuildInfo(Info{Version: "dev"}, &debug.BuildInfo{
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef1234567890"},
			{Key: "vcs.time", Value: "2026-01-02T03:04:05Z"},
		},
	})
	if got.Commit != "abcdef1234567890" || got.Date != "2026-01-02T03:04:05Z" {
		t.Errorf("VCS fallback = %+v, want commit/date filled", got)
	}
}

func TestFillFromBuildInfo_injectedNotClobbered(t *testing.T) {
	// Release build: the linker injected every field. Embedded build info must NOT
	// overwrite any of them — ldflags identity is authoritative.
	got := fillFromBuildInfo(
		Info{Version: "1.2.3", Commit: "injected-sha", Date: "injected-date", Go: "go1.x"},
		&debug.BuildInfo{
			Main: debug.Module{Version: "v9.9.9"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "other-sha"},
				{Key: "vcs.time", Value: "other-time"},
			},
		},
	)
	if got.Version != "1.2.3" || got.Commit != "injected-sha" || got.Date != "injected-date" {
		t.Errorf("injected identity was clobbered: %+v", got)
	}
}
