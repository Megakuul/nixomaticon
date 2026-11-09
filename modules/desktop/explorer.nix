# Nautilus / GTK file chooser: sort order and pinned sidebar folders.
{
  pkgs,
  config,
  ...
}: let
  home = config.home.homeDirectory;
in {
  home.packages = with pkgs; [
    filezilla
  ];
  xdg.userDirs = {
    enable = true;
    createDirectories = true;
  };

  # Newest thing on top, everywhere: Nautilus and every GTK file dialog (Qt
  # apps use the same dialog through the portal). No folders-first.
  dconf.settings = let
    chooser = {
      sort-column = "modified";
      sort-order = "descending";
      sort-directories-first = false;
    };
  in {
    "org/gnome/nautilus/preferences" = {
      default-sort-order = "mtime";
      default-sort-in-reverse-order = true; # newest first
    };
    "org/gtk/settings/file-chooser" = chooser; # GTK3 + xdg-desktop-portal-gtk
    "org/gtk/gtk4/settings/file-chooser" = chooser; # GTK4 + Nautilus
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
