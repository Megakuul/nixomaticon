{
  pkgs,
  config,
  ...
}: {
  # Nautilus on Fedora only loads extensions from its own dir; point it at the
  # profile so nautilus-python (and the python extensions below) load.
  home.sessionVariables.NAUTILUS_4_EXTENSION_DIR = "${config.home.profileDirectory}/lib/nautilus/extensions-4";
  # Fedora ships its own Nautilus; make D-Bus activation (folder links, "show in
  # folder") start this one, which loads the extensions above.
  xdg.dataFile = pkgs.lib.genAttrs ["org.gnome.Nautilus.service" "org.freedesktop.FileManager1.service"] (f: {
    target = "dbus-1/services/${f}";
    source = "${pkgs.nautilus}/share/dbus-1/services/${f}";
  });

  home.packages = with pkgs; [
    putty
    # multimedia
    gimp3-with-plugins
    obs-studio
    pavucontrol
    vlc
    kdePackages.kdenlive
    tenacity
    ksnip
    nautilus
    nautilus-python
    nautilus-open-any-terminal
    sushi
    loupe # image viewer
    peazip
    gvfs
    # right-click "Open in VSCodium" (upstream calls `code`)
    (code-nautilus.overrideAttrs (old: {
      postInstall =
        (old.postInstall or "")
        + ''
          substituteInPlace $out/share/nautilus-python/extensions/code-nautilus.py \
            --replace-fail "VSCODE = 'code'" "VSCODE = 'codium'" \
            --replace-fail "VSCODENAME = 'Code'" "VSCODENAME = 'VSCodium'"
        '';
    }))
    zenity

    # office: ArtCraft suite comes from dnf (bootstrap.sh / artcraft.sh)
    gnome-calculator

    # password
    # bitwarden-desktop
    # _1password-gui

    # utility
    gnome-disk-utility
    mission-center # GUI resources monitor

    # analysis
    ghidra
    wireshark
    kdePackages.kleopatra

    # virtualisation
    osinfo-db
    osinfo-db-tools
    virt-manager
    tigervnc

    # remotemanagement tooling
    remmina
  ];
}
