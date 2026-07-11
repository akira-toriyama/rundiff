module github.com/akira-toriyama/rundiff

// Floor pinned to a patched 1.26.x: go-version-file drives CI/release to build
// with exactly this toolchain, so the shipped binary carries current stdlib
// security fixes (govulncheck -mode binary enforces this in CI — bump on a red).
go 1.26.4

require github.com/spf13/cobra v1.10.2

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
)
