# Everything personal lives here. Rename `homeConfigurations.linus` in flake.nix
# if your Fedora username differs.
{...}: {
  home.username = "linus";
  home.homeDirectory = "/home/linus";
  home.stateVersion = "26.05";

  beast = {
    nixomaticon = true;
    flakePath = "/home/linus/Documents/repos/nixomaticon";
    profile.git = {
      username = "Linus Moser";
      email = "linus.moser@informaticon.com";
    };
    # teleport.proxy = "teleport.example.com:443";
  };
}
