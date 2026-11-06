{
  pkgs,
  config,
  ...
}: {
  home.packages = with pkgs; [ghostty];

  xdg.configFile."ghostty/config".text = ''
    # Login shell stays Fedora's bash (TTY/GDM must not depend on /nix)
    command = ${config.programs.fish.package}/bin/fish

    # Font
    font-family = "Maple Mono"
    font-size = 16
    font-feature = calt
    font-feature = ss03

    bold-is-bright = false
    selection-invert-fg-bg = true

    # Theme
    theme = "gruvbox"
    background-opacity = 0.66

    cursor-style = bar
    cursor-style-blink = false
    adjust-cursor-thickness = 1

    resize-overlay = never
    copy-on-select = false
    confirm-close-surface = false
    mouse-hide-while-typing = true

    window-theme = ghostty
    # window-padding-x = 4
    # window-padding-y = 6
    window-padding-balance = true
    window-padding-color = background
    window-inherit-working-directory = true
    window-inherit-font-size = true
    window-decoration = false

    gtk-titlebar = false
    gtk-single-instance = false
    gtk-tabs-location = bottom
    gtk-wide-tabs = false

    auto-update = off
    term = ghostty
    clipboard-paste-protection = false

    keybind = shift+end=unbind
    keybind = shift+home=unbind
    keybind = ctrl+shift+left=unbind
    keybind = ctrl+shift+right=unbind
    keybind = shift+enter=text:\n

    # same as kitty: ctrl+hjkl are arrow keys (vim-style completion and
    # cursor movement), ctrl+shift+h/l jump words, alt+1..8 switch tabs
    keybind = ctrl+h=text:\x1b[D
    keybind = ctrl+j=text:\x1b[B
    keybind = ctrl+k=text:\x1b[A
    keybind = ctrl+l=text:\x1b[C
    keybind = ctrl+shift+h=text:\x1b[1;5D
    keybind = ctrl+shift+l=text:\x1b[1;5C
    keybind = alt+1=goto_tab:1
    keybind = alt+2=goto_tab:2
    keybind = alt+3=goto_tab:3
    keybind = alt+4=goto_tab:4
    keybind = alt+5=goto_tab:5
    keybind = alt+6=goto_tab:6
    keybind = alt+7=goto_tab:7
    keybind = alt+8=goto_tab:8
  '';
  xdg.configFile."ghostty/themes/gruvbox".text = ''
    background = #1d2021
    foreground = #fbf1c7

    palette = 0=#32302f
    palette = 1=#cc241d
    palette = 2=#98971a
    palette = 3=#d79921
    palette = 4=#458588
    palette = 5=#b16286
    palette = 6=#689d6a
    palette = 7=#ebdbb2

    palette = 8=#928374
    palette = 9=#fb4934
    palette = 10=#b8bb26
    palette = 11=#fabd2f
    palette = 12=#83a598
    palette = 13=#d3869b
    palette = 14=#8ec07c
    palette = 15=#fbf1c7

    cursor-color = #D5C4A1

    selection-foreground = #282828
    selection-background = #98971A
  '';
}
