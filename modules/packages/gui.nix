{
  pkgs,
  config,
  inputs,
  ...
}: let
  # nixpkgs' Nautilus only gets gvfs's GIO module on NixOS (services.gvfs sets
  # GIO_EXTRA_MODULES globally). Without it smb://, sftp:// and the Network view
  # don't work and Nautilus just hangs. Setting it session-wide would also load
  # the nix module into Fedora's glib apps, so wrap only Nautilus. The module
  # talks to Fedora's gvfsd (gvfs-smb) over D-Bus.
  nautilus = pkgs.symlinkJoin {
    name = "nautilus-gvfs";
    paths = [pkgs.nautilus];
    nativeBuildInputs = [pkgs.makeWrapper];
    postBuild = ''
      wrapProgram $out/bin/nautilus \
        --prefix GIO_EXTRA_MODULES : ${pkgs.gvfs}/lib/gio/modules
      for f in $out/share/dbus-1/services/*.service; do
        substitute "$(readlink -f "$f")" "$f.tmp" --replace-fail ${pkgs.nautilus}/bin/nautilus $out/bin/nautilus
        mv "$f.tmp" "$f"
      done
    '';
  };
in {
  # Nautilus on Fedora only loads extensions from its own dir; point it at the
  # profile so nautilus-python (and the python extensions below) load.
  home.sessionVariables.NAUTILUS_4_EXTENSION_DIR = "${config.home.profileDirectory}/lib/nautilus/extensions-4";
  # Fedora ships its own Nautilus; make D-Bus activation (folder links, "show in
  # folder") start this one, which loads the extensions above.
  xdg.dataFile =
    pkgs.lib.genAttrs ["org.gnome.Nautilus.service" "org.freedesktop.FileManager1.service"]
    (f: {
      target = "dbus-1/services/${f}";
      source = "${nautilus}/share/dbus-1/services/${f}";
    });

  # ArtCraft (Adobe-style + office), only in the latest nixpkgs so far.
  # FilmCraft, EffectCraft and DesignCraft aren't packaged there yet.
  home.packages =
    (with inputs.nixpkgs-latest.legacyPackages.${pkgs.stdenv.hostPlatform.system}; [
      photocraft # Photoshop
      vectorcraft # Illustrator
      lightcraft # Lightroom
      pdfcraft # Acrobat
      wordcraft # Word
      gridcraft # Excel
      deckcraft # PowerPoint
    ])
    ++ (with pkgs; [
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
    ]);
}
