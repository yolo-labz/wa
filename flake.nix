{
  description = "wa — WhatsApp automation CLI + daemon (hexagonal Go, per-profile isolation)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    self,
    nixpkgs,
    flake-utils,
    ...
  }: let
    # Version is injected via ldflags. If the flake is built from a
    # dirty tree the derivation gets `0.0.0-dirty`; when built from a
    # clean git ref (tag or commit) it becomes `self.shortRev` and the
    # resulting binary reports it via `wa --version`.
    versionFor = self:
      if self ? shortRev
      then self.shortRev
      else "0.0.0-dirty";

    # Cross-platform systems output. eachDefaultSystem wraps the per-
    # system block below so we get packages/apps/checks/devShells for
    # every default system (x86_64-linux, aarch64-linux, x86_64-darwin,
    # aarch64-darwin).
    perSystem = flake-utils.lib.eachDefaultSystem (system: let
      pkgs = nixpkgs.legacyPackages.${system};
      version = versionFor self;

      # The Go module build for both binaries. subPackages constrains
      # the build to the two main packages we ship, avoiding wasted
      # compilation of internal test harnesses. CGO is disabled per
      # constitution §IV — `modernc.org/sqlite` is the only SQLite
      # path.
      wa = pkgs.buildGoModule {
        pname = "wa";
        inherit version;
        src = ./.;

        # IMPORTANT: this hash is derived from go.sum. Bumping any
        # dependency in go.mod / go.sum requires recomputing it:
        #
        #   1. Set vendorHash = lib.fakeHash (or comment out)
        #   2. Run `nix build .#default`
        #   3. Copy the hash from the "got" line in the error
        #   4. Paste it back here
        #
        # Or use nixpkgs' `lib.fakeSha256` + `nix-prefetch`. Renovate
        # cannot update this automatically, so bumps are gated by a
        # manual `nix build` on the feature branch.
        vendorHash = "sha256-8XLezzfNjp6OzjjThNZMmhf1K/e6j17L57lei9IhpFo=";

        subPackages = ["cmd/wa" "cmd/wad"];

        # Constitution §IV: CGO is forbidden. modernc.org/sqlite is
        # the only SQLite path; every Go dependency in go.sum is
        # CGO-free.
        env.CGO_ENABLED = "0";

        ldflags = [
          "-s"
          "-w"
          "-X main.version=${version}"
          "-X main.commit=${self.rev or "dirty"}"
          "-X main.date=1970-01-01T00:00:00Z"
        ];

        # Don't run Go's vet as part of the Nix build — CI covers it,
        # and vet pulls in the full toolchain which adds 30+ seconds.
        doCheck = false;

        meta = with pkgs.lib; {
          description = "WhatsApp automation CLI + daemon (per-profile isolation)";
          longDescription = ''
            wa is a personal-account WhatsApp automation tool built on
            top of go.mau.fi/whatsmeow. It consists of two binaries:

              - wad: long-running daemon that owns the WhatsApp session,
                the SQLite ratchet store, and the websocket. Runs as a
                systemd user service (Linux) or launchd agent (macOS).

              - wa:  thin JSON-RPC client that speaks to wad over a
                unix socket. This is what shell scripts and Claude Code
                plugins actually invoke.

            Feature 008 adds per-profile isolation: multiple WhatsApp
            accounts run side-by-side with their own session.db,
            allowlist, audit log, rate limiter, and socket.
          '';
          homepage = "https://github.com/yolo-labz/wa";
          changelog = "https://github.com/yolo-labz/wa/releases/tag/v${version}";
          license = licenses.asl20;
          maintainers = [];
          mainProgram = "wa";
          platforms = platforms.linux ++ platforms.darwin;
        };
      };
    in {
      # `nix build .#default` / `nix build` produces both binaries
      # under result/bin/. `.#wa` and `.#wad` are aliases for the same
      # derivation (they both resolve to the multi-binary output).
      packages.default = wa;
      packages.wa = wa;
      packages.wad = wa;

      # `nix run .#wa -- send --to ...` and `nix run .#wad` work out
      # of the box.
      apps.default = {
        type = "app";
        program = "${wa}/bin/wa";
      };
      apps.wa = {
        type = "app";
        program = "${wa}/bin/wa";
      };
      apps.wad = {
        type = "app";
        program = "${wa}/bin/wad";
      };

      # `nix flake check` runs these. The "build" check reuses the
      # default package and is therefore cached after the first build;
      # CI can run `nix flake check` with almost no incremental cost
      # once the store is warmed.
      checks = {
        build = wa;
      };

      devShells.default = pkgs.mkShell {
        packages = with pkgs; [
          go
          gopls
          gotools
          gofumpt
          golangci-lint
          goreleaser
          git-cliff
          lefthook
          sqlite
          jq
        ];

        shellHook = ''
          echo "wa devshell — go $(go version | awk '{print $3}')"
          echo "  build:    go build ./cmd/wa ./cmd/wad"
          echo "  test:     go test -race ./..."
          echo "  lint:     golangci-lint run"
          echo "  format:   gofumpt -w ."
          echo "  nix:      nix build .#default && ./result/bin/wa --version"
          echo "  release:  goreleaser release --snapshot --clean --skip=publish"
        '';
      };

      formatter = pkgs.alejandra;
    });
  in
    perSystem
    // {
      # NixOS module. Users add `inputs.wa.nixosModules.default` to
      # their flake and enable via `services.wa.enable = true`. The
      # module installs the wad binary, creates a dedicated system
      # user, and provides a systemd service that runs wad with a
      # per-profile --profile flag.
      nixosModules.default = import ./nix/nixos-module.nix self;
      nixosModules.wa = import ./nix/nixos-module.nix self;

      # home-manager module. The opposite end of the same spectrum:
      # runs wad as a user-level systemd service in the user's home
      # dir, which is how most individual users will deploy it.
      homeManagerModules.default = import ./nix/home-manager-module.nix self;
      homeManagerModules.wa = import ./nix/home-manager-module.nix self;

      # Overlay for users who want to inject the package into an
      # existing nixpkgs overlay stack: `wa.overlays.default`.
      overlays.default = _final: prev: {
        wa = self.packages.${prev.system}.default;
      };
    };
}
