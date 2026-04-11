{
  description = "wa - WhatsApp automation CLI + daemon";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    nixpkgs,
    flake-utils,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (system: let
      pkgs = nixpkgs.legacyPackages.${system};
    in {
      packages.default = pkgs.buildGoModule {
        pname = "wa";
        version = "0.1.0";
        src = ./.;
        vendorHash = pkgs.lib.fakeHash; # replace after first `nix build` prints the real hash
        subPackages = ["cmd/wa" "cmd/wad"];
        env.CGO_ENABLED = 0;
        ldflags = ["-s" "-w" "-X main.version=0.1.0"];
        meta = with pkgs.lib; {
          description = "WhatsApp automation CLI + daemon";
          homepage = "https://github.com/yolo-labz/wa";
          license = licenses.asl20;
          mainProgram = "wa";
        };
      };

      devShells.default = pkgs.mkShell {
        packages = with pkgs; [
          go
          gopls
          golangci-lint
          goreleaser
          git-cliff
          lefthook
          sqlite
        ];
      };
    });
}
