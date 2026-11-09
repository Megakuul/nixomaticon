{
  config,
  pkgs,
  ...
}: let
  kitty = config.programs.kitty.package;
  hyprctl = "${config.wayland.windowManager.hyprland.finalPackage}/bin/hyprctl";
  # Opens files in nvim as a new tab of the most recent kitty (and focuses it),
  # or in a new kitty when none is running. Used for text files in Nautilus.
  nvimTab = pkgs.writeShellScriptBin "nvim-tab" ''
    cwd=$(dirname -- "''${1:-$HOME/.}")
    for sock in $(ls -t "$XDG_RUNTIME_DIR"/kitty-* 2>/dev/null); do
      if ${kitty}/bin/kitten @ --to "unix:$sock" launch --type=tab --cwd="$cwd" ${pkgs.neovim}/bin/nvim "$@" >/dev/null 2>&1; then
        ${hyprctl} dispatch focuswindow "pid:''${sock##*-}" >/dev/null 2>&1
        exit 0
      fi
    done
    exec ${kitty}/bin/kitty --directory "$cwd" ${pkgs.neovim}/bin/nvim "$@"
  '';
in {
  home.packages = [nvimTab];

  xdg.desktopEntries.nvim-tab = {
    name = "Neovim (kitty tab)";
    exec = "nvim-tab %F";
    icon = "nvim";
    terminal = false;
    noDisplay = true; # only for "Open with", not the app launcher
    mimeType = ["text/plain"];
  };

  programs.kitty = {
    enable = true;

    themeFile = "gruvbox-dark-hard";

    font = {
      name = "Maple Mono";
      size = 16;
    };

    extraConfig = ''
      font_features MapleMono-Regular +ss01 +ss02 +ss04
      font_features MapleMono-Bold +ss01 +ss02 +ss04
      font_features MapleMono-Italic +ss01 +ss02 +ss04
      font_features MapleMono-Light +ss01 +ss02 +ss04

      # nvim sets the in_editor user var (see config/nvim/lua/mappings.lua);
      # there ctrl+hjkl reach nvim itself instead of the arrow keys below, so
      # they switch splits (NvChad's <C-hjkl>).
      map --when-focus-on var:in_editor ctrl+h
      map --when-focus-on var:in_editor ctrl+j
      map --when-focus-on var:in_editor ctrl+k
      map --when-focus-on var:in_editor ctrl+l
    '';

    settings = {
      # control socket for nvim-tab, one per kitty, only reachable by this user
      allow_remote_control = "socket-only";
      listen_on = "unix:\${XDG_RUNTIME_DIR}/kitty-{kitty_pid}";
      shell = "${config.programs.fish.package}/bin/fish";
      confirm_os_window_close = 0;
      background_opacity = "0.66";
      scrollback_lines = 10000;
      enable_audio_bell = false;
      mouse_hide_wait = 60;
      window_padding_width = 5;

      ## Tabs
      tab_title_template = "{index}";
      active_tab_font_style = "normal";
      inactive_tab_font_style = "normal";
      tab_bar_style = "powerline";
      tab_powerline_style = "angled";
      active_tab_foreground = "#FBF1C7";
      active_tab_background = "#7C6F64";
      inactive_tab_foreground = "#FBF1C7";
      inactive_tab_background = "#3C3836";
    };

    keybindings = {
      ## Tabs
      "alt+1" = "goto_tab 1";
      "alt+2" = "goto_tab 2";
      "alt+3" = "goto_tab 3";
      "alt+4" = "goto_tab 4";
      "alt+5" = "goto_tab 5";
      "alt+6" = "goto_tab 6";
      "alt+7" = "goto_tab 7";
      "alt+8" = "goto_tab 8";

      "ctrl+h" = "send_text all \\x1b[D";
      "ctrl+j" = "send_text all \\x1b[B";
      "ctrl+k" = "send_text all \\x1b[A";
      "ctrl+l" = "send_text all \\x1b[C";
      "shift+ctrl+h" = "send_text all \\e[1;5D";
      "shift+ctrl+l" = "send_text all \\e[1;5C";

      ## Font zoom (the default ctrl+shift+equal/plus need Shift anyway on the CH layout)
      "ctrl+shift+up" = "change_font_size all +2.0";
      "ctrl+shift+down" = "change_font_size all -2.0";
      "ctrl+shift+backspace" = "change_font_size all 0";

      ## Unbind
      "ctrl+shift+left" = "no_op";
      "ctrl+shift+right" = "no_op";
    };
  };
}
