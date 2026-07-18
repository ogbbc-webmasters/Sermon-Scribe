{
  description = "Sermon Scribe development environment";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { nixpkgs, ... }:
    let
      system = "x86_64-linux";
      pkgs = import nixpkgs { inherit system; };

      elm = pkgs.stdenvNoCC.mkDerivation {
        pname = "elm";
        version = "0.19.2";

        src = pkgs.fetchurl {
          url = "https://github.com/elm/compiler/releases/download/0.19.2/elm-0.19.2-linux-x64.gz";
          hash = "sha256-ZjINJ3AWVPoRvQ6NhL35gpaU1XcMjc7i3t5hYPrVhzc=";
        };

        dontUnpack = true;
        nativeBuildInputs = [ pkgs.gzip ];

        installPhase = ''
          runHook preInstall
          mkdir -p "$out/bin"
          gzip -dc "$src" > "$out/bin/elm"
          chmod +x "$out/bin/elm"
          runHook postInstall
        '';
      };
    in
    {
      devShells.${system}.default = pkgs.mkShell {
        packages = [
          elm
          pkgs.ffmpeg
          pkgs.go
          pkgs.just
        ];
      };
    };
}
