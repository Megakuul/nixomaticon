# Same knobs as beastos' NixOS `beast.*` options, but on the home-manager side.
{
  config,
  lib,
  ...
}: {
  options.beast = {
    nixomaticon = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Enable informaticon tools";
    };
    flakePath = lib.mkOption {
      type = lib.types.str;
      default = "${config.home.homeDirectory}/nixomaticon";
      description = "Where this flake is checked out (used by nh)";
    };
    teleport.proxy = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "teleport.example.com:443";
      description = "Default Teleport proxy for tsh (TELEPORT_PROXY)";
    };
    profile = {
      git = {
        username = lib.mkOption {
          type = lib.types.str;
          description = "Git username";
        };
        email = lib.mkOption {
          type = lib.types.str;
          description = "Git email";
        };
        configPath = lib.mkOption {
          type = lib.types.str;
          description = "Path to git configurations and keys";
          default = "~/.config/git";
        };
      };
      windowrules = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [];
        description = "List of hyprland window rules";
      };
      startup = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [];
        description = "List of startup commands";
      };
      apps = {
        browser = lib.mkOption {
          type = lib.types.str;
          default = "brave";
          description = "Default system browser";
        };
        terminal = lib.mkOption {
          type = lib.types.str;
          default = "ghostty";
          description = "Default system terminal";
        };
        explorer = lib.mkOption {
          type = lib.types.str;
          default = "nautilus";
          description = "Default system explorer";
        };
      };
    };
  };
}
