# Everything personal lives here. Rename `homeConfigurations.linus` in flake.nix
# if your Fedora username differs.
{...}: {
  home.username = "linus";
  home.homeDirectory = "/home/linus";
  home.stateVersion = "26.05";

  beast = {
    nixomaticon = true;
    flakePath = "/home/linus/Documents/repos/nixomaticon";
    profile.apps.terminal = "kitty"; # SUPER+Q
    profile.git = {
      username = "megakuul";
      email = "linus.moser@megakuul.ch";
    };
    # teleport.proxy = "teleport.example.com:443";
  };
}
