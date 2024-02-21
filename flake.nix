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
      lib = pkgs.lib;
    in
    {
      packages = rec {
        sync-server = pkgs.buildGo122Module rec {
          pname = "sync-server";
          version = "0.0.0";
          src = ./.;
          proxyVendor = true;
          vendorHash = "sha256-XQTpAcL9Aatk58QIClbdgbuDQRJpN83PjhYWHm+AuCA=";
          subPackages = [ "server" ];
          CGO_ENABLED = "0";
          ldflags = [ "-s" "-w" ];
          meta.mainProgram = "server";
        };
        sync-server-container = pkgs.dockerTools.buildImage {
          name = "sync-server";
          tag = "v${sync-server.version}";
          copyToRoot = with pkgs.dockerTools; [ caCertificates ];
          config = {
            Cmd = [ "${lib.getExe sync-server}" ];
          };
        };
        push-sync-server-container = pkgs.writeShellApplication {
          name = "push-sync-server-container";
          runtimeInputs = with pkgs; [ crane gzip ];
          text = ''
            imageName="$1"
            tmpdir=$(mktemp -d)
            trap 'rm -rf $tmpdir' EXIT
            gunzip < "${sync-server-container}" > "$tmpdir/image.tar"
            crane push "$tmpdir/image.tar" "$imageName:${sync-server-container.imageTag}"
          '';
        };
      };

      devShells.default = pkgs.mkShell {
        packages = with pkgs; [
          golangci-lint
          go_1_22
        ];
        shellHook = ''alias make=just'';
      };
    });
}
