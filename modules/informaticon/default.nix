# informaticon specific software that is not particularly stable and may change in the future
{
  config,
  lib,
  pkgs,
  inputs,
  ...
}: let
  system = pkgs.stdenv.hostPlatform.system;
in {
  imports = [
    ./twinkle.nix
  ];

  config = lib.mkIf config.beast.nixomaticon {
    home.packages = [
      # developed in-house under ./custom
      inputs.voiper.packages.${system}.default
      inputs.vibebender.packages.${system}.default # replaces nxbender
    ];

    # teleport_18 (tsh, tctl, tbot) itself is installed in packages/cli.nix
    home.sessionVariables = lib.mkIf (config.beast.teleport.proxy != null) {
      TELEPORT_PROXY = config.beast.teleport.proxy;
    };
  };
}
