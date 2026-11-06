{
  description = "Nixomaticon - beastos home-manager setup for Fedora";

  inputs = {
    # One nixpkgs for everything: kiwi-shell's pin. kiwi's Hyprland plugin must
    # match the compositor, and mixing two nixpkgs breaks glibc (Mesa, GLib).
    # `nix flake update` moves it whenever kiwi-shell updates its lock.
    nixpkgs.follows = "selimbar/nixpkgs";

    # Latest unstable, only for packages the main pin doesn't have yet
    # (ArtCraft apps). Their prebuilt binaries come from cache.nixos.org.
    nixpkgs-latest.url = "github:NixOS/nixpkgs/nixos-unstable";

    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    # kiwi-shell builds against its own (tested) nixpkgs
    selimbar.url = "github:selimbucher/kiwi-shell";
    whitesur-src = {
      url = "github:vinceliuice/WhiteSur-icon-theme";
      flake = false;
    };
    slimmer-icons = {
      url = "github:selimbucher/WhiteSur-steam-icons";
      flake = false;
    };

    native-instruments = {
      url = "github:selimbucher/native-instruments";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    # In-house projects, vendored into ./custom.
    voiper.url = "path:./custom/voiper";
    vibebender = {
      url = "path:./custom/vibebender";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = {...} @ inputs: let
    system = "x86_64-linux";
    pkgs = import inputs.nixpkgs {
      inherit system;
      config.allowUnfree = true;
    };
    theme = {
      profile = "${./assets/profile.png}";
      wallpaper = "${./assets/white.jpg}";
      lockpaper = "${./assets/white.jpg}";
    };
  in {
    homeConfigurations.linus = inputs.home-manager.lib.homeManagerConfiguration {
      inherit pkgs;
      extraSpecialArgs = {inherit inputs theme;};
      modules = [
        inputs.selimbar.homeManagerModules.default
        ./modules
        ./profile.nix
      ];
    };

    # pinned CLI for the first switch: nix run .#home-manager -- switch --flake .
    packages.${system}.home-manager = inputs.home-manager.packages.${system}.home-manager;

    formatter.${system} = pkgs.alejandra;
  };
}
