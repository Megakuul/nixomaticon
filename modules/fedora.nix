# What beastos' NixOS core modules did, translated for home-manager on Fedora.
# Root-level leftovers (GDM session, groups, docker, libvirt, swaylock)
# are handled once by ./bootstrap.sh.
{
  config,
  pkgs,
  ...
}: let
  session = pkgs.writeShellScriptBin "hyprland-session" ''
    . "${config.home.profileDirectory}/etc/profile.d/hm-session-vars.sh"
    exec ${config.wayland.windowManager.hyprland.finalPackage}/bin/start-hyprland "$@"
  '';
in {
  targets.genericLinux.enable = true; # XDG_DATA_DIRS, fonts, GPU drivers (non-nixos-gpu-setup)
  programs.home-manager.enable = true;

  home.packages = with pkgs; [
    session
    nix-output-monitor
    nvd
    networkmanagerapplet
    poweralertd
    brightnessctl
    system-config-printer
    virtio-win
    win-spice
  ];

  # nh (beastos core/nh.nix): `nh home switch`
  programs.nh = {
    enable = true;
    flake = config.beast.flakePath;
    clean = {
      enable = true;
      extraArgs = "--keep-since 7d --keep 5";
    };
  };

  # beastos core/program.nix: gnupg agent with ssh support
  services.gpg-agent = {
    enable = true;
    enableSshSupport = true;
    pinentry.package = pkgs.pinentry-gnome3;
  };

  # beastos core/wayland.nix (the hyprland portal is added by the hyprland module)
  xdg.portal = {
    enable = true;
    xdgOpenUsePortal = true;
    extraPortals = [pkgs.xdg-desktop-portal-gtk];
    config = {
      common.default = ["gtk"];
      hyprland.default = ["gtk" "hyprland"];
    };
  };
  home.sessionVariables.GTK_IM_MODULE = "simple"; # fixes gtk dead-key issues

  # pkexec prompts (vibebender) need an agent inside Hyprland
  services.hyprpolkitagent.enable = true;
}
