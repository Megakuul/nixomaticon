{
  description = "Vibebender - SonicWall NetExtender VPN client with a tiny GUI";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = {self, ...} @ inputs: let
    systems = [
      "x86_64-linux"
      "aarch64-linux"
    ];
    forAll = f: inputs.nixpkgs.lib.genAttrs systems (system: f inputs.nixpkgs.legacyPackages.${system});
  in {
    packages = forAll (pkgs: rec {
      vibebender = pkgs.buildGoModule {
        pname = "vibebender";
        version = "0.2.0";
        src = inputs.nixpkgs.lib.cleanSourceWith {
          src = inputs.nixpkgs.lib.cleanSource ./.;
          filter = path: _: !(builtins.elem (baseNameOf path) ["build" "wailsjs" "result"]);
        };
        vendorHash = "sha256-bmLG/Q0fSC6zgtBVKaV55K/lCQlMgpLrede1Vc8XHsc=";

        tags = ["desktop" "production" "webkit2_41"];
        ldflags = ["-s" "-w" "-X main.pppdPath=${pkgs.ppp}/bin/pppd"];
        env.CGO_ENABLED = "1";
        # checkPhase runs the helper protocol tests against the fake session

        nativeBuildInputs = with pkgs; [pkg-config wrapGAppsHook3 copyDesktopItems];
        buildInputs = with pkgs; [gtk3 webkitgtk_4_1];

        desktopItems = [
          (pkgs.makeDesktopItem {
            name = "vibebender";
            desktopName = "Vibebender";
            genericName = "VPN Client";
            comment = "SonicWall NetExtender VPN";
            exec = "vibebender";
            icon = "network-vpn";
            keywords = ["netextender" "sonicwall" "vpn" "nxbender"];
            categories = ["Network"];
            startupWMClass = "vibebender";
          })
        ];

        meta = {
          description = "SonicWall NetExtender VPN client with a tiny GUI";
          homepage = "https://github.com/megakuul/vibebender";
          mainProgram = "vibebender";
          platforms = systems;
        };
      };
      default = vibebender;
    });

    checks = forAll (pkgs: {
      vibebender = self.packages.${pkgs.stdenv.hostPlatform.system}.vibebender;
    });

    formatter = forAll (pkgs: pkgs.alejandra);

    devShells = forAll (pkgs: {
      default = pkgs.mkShell {
        inputsFrom = [self.packages.${pkgs.stdenv.hostPlatform.system}.vibebender];
        packages = with pkgs; [
          gnumake
          go
          gotools
          go-tools
          gofumpt
          gopls
          wails
          ppp
        ];
        GOFLAGS = "-tags=desktop,production,webkit2_41";
      };
    });
  };
}
