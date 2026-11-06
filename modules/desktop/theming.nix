# let the linux desktop theming mess begin
{
  config,
  pkgs,
  inputs,
  lib,
  ...
}: let
  # GTK, Qt (Kvantum) and KDE apps all use Dracula from the same package.
  theme = pkgs.dracula-theme;
  iconTheme = "WhiteSur";
  # KDE apps (kdenlive, kleopatra, ...) take colors and icons from kdeglobals,
  # not from qt6ct, so give them Dracula's KDE color scheme.
  kdeglobals = pkgs.runCommand "kdeglobals" {} ''
    cat ${theme}/share/color-schemes/Dracula.colors > $out
    printf '\n[General]\nColorScheme=Dracula\nfont=Quicksand,12,-1,5,50,0,0,0,0,0\n\n[Icons]\nTheme=${iconTheme}\n\n[KDE]\nwidgetStyle=kvantum\n' >> $out
  '';
  qtct = {
    Appearance = {
      style = "kvantum";
      custom_palette = false; # palette comes from Kvantum
      icon_theme = iconTheme;
      # file pickers etc. through xdg-desktop-portal-gtk, like GTK apps
      standard_dialogs = "xdgdesktopportal";
    };
    Fonts = {
      general = ''"Quicksand,12"'';
      fixed = ''"DejaVu Sans Mono,11"'';
    };
  };
in {
  fonts.fontconfig = {
    enable = true;
    defaultFonts = {
      sansSerif = ["Quicksand"];
      monospace = ["DejaVu Sans Mono"];
    };
  };
  home.packages = with pkgs; [
    dejavu_fonts
    nerd-fonts.jetbrains-mono
    nerd-fonts.fira-code
    nerd-fonts.caskaydia-cove
    nerd-fonts.symbols-only
    quicksand
    twemoji-color-font
    noto-fonts-color-emoji
    fantasque-sans-mono
    maple-mono.truetype-autohint
    nerd-fonts.iosevka
    iosevka
  ];

  dconf.settings = {
    "org/gnome/desktop/interface" = {
      color-scheme = "prefer-dark";
    };
  };

  gtk = {
    enable = true;
    gtk4.theme = config.gtk.theme;
    font = {
      name = "Quicksand";
      size = 12;
    };
    theme = {
      name = "Dracula";
      package = theme;
    };
    iconTheme = {
      name = iconTheme;
      package =
        # this was not my idea please call an ambulance or @selimbucher to get rid of the pain
        (
          let
            base = pkgs.whitesur-icon-theme.override {
              alternativeIcons = true;
              boldPanelIcons = true;
            };
          in
            base.overrideAttrs (oldAttrs: {
              version = "latest";
              src = inputs.whitesur-src;
              myCustomIcons = inputs.slimmer-icons;
              dontCheckForBrokenSymlinks = true;
              postInstall =
                (oldAttrs.postInstall or "")
                + ''
                  cp -rf ${inputs.slimmer-icons}/apps/* $out/share/icons/WhiteSur/apps/

                  if [ -d "${inputs.slimmer-icons}/apps@2x" ]; then
                    cp -rf ${inputs.slimmer-icons}/apps@2x/* $out/share/icons/WhiteSur/apps@2x/
                  fi
                '';
            })
        );
    };
    cursorTheme = {
      name = "Bibata-Modern-Ice";
      package = pkgs.bibata-cursors;
      size = 24;
    };
  };

  # Qt5 and Qt6 apps: qt5ct/qt6ct as platform theme, Kvantum as style.
  # Dracula-Solid is Dracula without Kvantum's translucency and blur, which
  # only work under KWin; on Hyprland they leave menus and popups see-through.
  qt = {
    enable = true;
    platformTheme.name = "qtct";
    style.name = "kvantum";
    kvantum = {
      enable = true;
      themes = [theme];
      settings.General.theme = "Dracula-Solid";
    };
    qt5ctSettings = qtct;
    qt6ctSettings = qtct;
  };

  xdg.configFile."kdeglobals".source = kdeglobals;

  home.pointerCursor = {
    enable = true;
    name = "Bibata-Modern-Ice";
    package = pkgs.bibata-cursors;
    size = 24;
  };

  home.sessionVariables = {
    NIXOS_OZONE_WL = 1;
    __GL_GSYNC_ALLOWED = 0;
    __GL_VRR_ALLOWED = 0;
    _JAVA_AWT_WM_NONEREPARENTING = 1;
    DISABLE_QT5_COMPAT = 0;
    GDK_BACKEND = "wayland";
    ANKI_WAYLAND = 1;
    DIRENV_LOG_FORMAT = "";
    WLR_DRM_NO_ATOMIC = 1;
    QT_AUTO_SCREEN_SCALE_FACTOR = 1;
    QT_WAYLAND_DISABLE_WINDOWDECORATION = 1;
    QT_QPA_PLATFORM = "wayland;xcb";
    QT_QPA_PLATFORMTHEME = lib.mkForce "qt5ct";
    QT_STYLE_OVERRIDE = lib.mkForce "kvantum";
    GTK_THEME = "Dracula";
    MOZ_ENABLE_WAYLAND = 1;
    WLR_BACKEND = "vulkan";
    WLR_RENDERER = "vulkan";
    WLR_NO_HARDWARE_CURSORS = 1;
    XDG_CURRENT_DESKTOP = "Hyprland";
    XDG_SESSION_TYPE = "wayland";
    XDG_SESSION_DESKTOP = "Hyprland";
    SDL_VIDEODRIVER = "wayland";
    CLUTTER_BACKEND = "wayland";
    GRIMBLAST_HIDE_CURSOR = 0;
    EDITOR = "nvim";
  };
}
