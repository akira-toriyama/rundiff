{
  # rundiff — `nix run github:akira-toriyama/rundiff` or `nix profile install`.
  #
  # The primary distribution is the Homebrew cask (see .goreleaser.yaml); this
  # flake is the secondary, source-built channel. version stays "dev" on purpose
  # — a source build has no release number, so there is nothing to go stale (the
  # commit is stamped from the flake's own git rev instead).
  #
  # vendorHash pins the vendored go modules; when go.mod/go.sum change, set it
  # back to pkgs.lib.fakeHash, run `nix build`, and paste the hash nix prints
  # ("got: sha256-...").
  description = "Diff a command's output against its previous run — fixed/new/unchanged for AI coding agents";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        version = "dev";
        rev = self.rev or self.dirtyRev or "unknown";
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "rundiff";
          inherit version;
          src = ./.;
          # Placeholder: set to pkgs.lib.fakeHash, run `nix build`, and paste the
          # hash nix prints ("got: sha256-..."). CI does not nix-build, so this is
          # filled in out of band before relying on `nix run`.
          vendorHash = pkgs.lib.fakeHash;
          ldflags = [
            "-s" "-w"
            "-X github.com/akira-toriyama/rundiff/internal/version.Version=${version}"
            "-X github.com/akira-toriyama/rundiff/internal/version.Commit=${rev}"
          ];
          subPackages = [ "cmd/rundiff" ];
          meta = with pkgs.lib; {
            description = "Diff a command's output against its previous run — fixed/new/unchanged for AI coding agents";
            homepage = "https://github.com/akira-toriyama/rundiff";
            license = licenses.mit;
            mainProgram = "rundiff";
          };
        };

        apps.default = flake-utils.lib.mkApp {
          drv = self.packages.${system}.default;
          name = "rundiff";
        };

        devShells.default = pkgs.mkShell {
          packages = [ pkgs.go pkgs.golangci-lint pkgs.goreleaser pkgs.git-cliff ];
        };
      });
}
