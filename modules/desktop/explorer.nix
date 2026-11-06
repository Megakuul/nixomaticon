# Pinned folders in the Nautilus / GTK file chooser sidebar.
{config, ...}: let
  home = config.home.homeDirectory;
in {
  xdg.userDirs = {
    enable = true;
    createDirectories = true;
  };

  gtk.gtk3.bookmarks = map (dir: "file://${home}/${dir} ${baseNameOf dir}") [
    "Desktop"
    "Downloads"
    "Documents"
    "Pictures"
    "Music"
    "Videos"
    "Documents/repos"
  ];
}
