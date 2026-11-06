# Language toolchains (go, rust, node, python, c/c++, ocaml, flutter, ...)
# come from each project's dev flake via direnv, e.g. custom/voiper. Only the
# tooling for this config, shell scripts and the editor configs stays global.
{pkgs, ...}: {
  home.packages = with pkgs; [
    # lua (nvim config)
    stylua
    lua-language-server

    # nix
    alejandra
    nixd
    nixfmt

    # yaml de-retarders
    yamlfmt
    yaml-language-server

    # bash
    bash-language-server
    shfmt

    # one-off python scripts (uv fetches interpreters itself)
    uv
  ];
}
