{
  description = "Voiper Linux SIP desktop client";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  outputs = {
    self,
    nixpkgs,
  }: let
    system = "x86_64-linux";
    pkgs = import nixpkgs {inherit system;};
    go = pkgs.go_1_27;
    nodejs = pkgs.nodejs_24;
    goBuilder = pkgs.buildGoModule.override {inherit go;};
    staticcheck = pkgs.go-tools.override {buildGoModule = goBuilder;};
    pnpm = pkgs.pnpm;
    baresip = pkgs.baresip.overrideAttrs (old: {
      buildInputs = old.buildInputs ++ [pkgs.libopus];
      postInstall =
        (old.postInstall or "")
        + ''
          test -f "$out/lib/baresip/modules/opus.so"
        '';
    });
    # Keep the shell's codec headers when the Wails CLI invokes Go.
    wails = (pkgs.wails.override {inherit go nodejs;}).overrideAttrs (old: {
      postFixup =
        builtins.replaceStrings ["--set PKG_CONFIG_PATH"] ["--suffix PKG_CONFIG_PATH :"]
        old.postFixup;
    });
    voiper = pkgs.callPackage ./nix/package.nix {
      inherit go nodejs pnpm;
      buildGoModule = pkgs.buildGoModule.override {inherit go;};
    };
  in {
    packages.${system} = {
      inherit voiper;
      default = voiper;
    };
    apps.${system}.default = {
      type = "app";
      program = "${voiper}/bin/voiper";
      meta.description = "Voiper SIP softphone";
    };
    checks.${system} = {inherit voiper;};
    formatter.${system} = pkgs.nixfmt;
    devShells.${system}.default = pkgs.mkShell {
      packages = with pkgs; [
        go
        nodejs
        pnpm
        wails
        pkg-config
        gopls
        gofumpt
        staticcheck
        govulncheck
        nixfmt
        sipp
        baresip
        pulseaudio
        pipewire
        wireplumber
        alsa-utils
      ];
      inputsFrom = [voiper];
      GOTOOLCHAIN = "local";
      CGO_ENABLED = "1";
      GOFLAGS = "-tags=webkit2_41,opus,speex,secretservice";
      LD_LIBRARY_PATH = pkgs.lib.makeLibraryPath [
        pkgs.libpulseaudio
        pkgs.alsa-lib
      ];
    };
  };
}
