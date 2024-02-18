{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    { self
    , nixpkgs
    , flake-utils
    }:
    flake-utils.lib.eachDefaultSystem (system:
    let
      pkgs = import nixpkgs { inherit system; };
    in
    {

      devShells.default = pkgs.mkShell {
        packages = with pkgs; [
          golangci-lint
          go_1_22
        ];
        shellHook = ''alias make=just'';
      };
    });
}
