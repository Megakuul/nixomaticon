{
  config,
  pkgs,
  ...
}:
{
  home.packages = [ pkgs.gh ];

  programs.git = {
    enable = true;

    settings = {
      user.name = "${config.beast.profile.git.username}";
      user.email = "${config.beast.profile.git.email}";
      user.signingkey = "${config.beast.profile.git.configPath}/signingkey";
      gpg.format = "ssh";
      commit.gpgsign = true;
      init.defaultBranch = "main";
      merge.conflictstyle = "diff3";
      diff.colorMoved = "default";
      pull.ff = "only";
      color.ui = true;
      core.excludesFile = "${config.beast.profile.git.configPath}/.gitignore";
      credential.helper = "!${pkgs.gh}/bin/gh auth git-credential";
      url = {
        "git@github.com:" = {
          insteadOf = "https://github.com/";
        };
        "git@codeberg.org:" = {
          insteadOf = "https://codeberg.org/";
        };
      };
    };
  };
  programs.ssh = {
    enable = true;
    enableDefaultConfig = false;
    includes = ["config.d/*"];
    matchBlocks = {
      "*" = {

      };
      "github.com" = {
        hostname = "github.com";
        identityFile = "${config.beast.profile.git.configPath}/github";
        identitiesOnly = true;
        addKeysToAgent = "yes";
      };
      "codeberg.org" = {
        hostname = "codeberg.org";
        identityFile = "${config.beast.profile.git.configPath}/codeberg";
        identitiesOnly = true;
        addKeysToAgent = "yes";
      };
    };
  };
  programs.delta = {
    enable = true;
    enableGitIntegration = true;
    options = {
      line-numbers = true;
      side-by-side = false;
      diff-so-fancy = true;
      navigate = true;
    };
  };

  xdg.configFile."git/.gitignore".text = ''
    .vscode
  '';
}
