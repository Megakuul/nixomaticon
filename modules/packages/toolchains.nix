# Language toolchains (go, rust, node, python, c/c++, ocaml, flutter, ...)
# come from each project's dev flake via direnv, e.g. custom/voiper. Language
# servers and formatters for neovim live in advanced/nvim.nix.
{pkgs, ...}: {
  home.packages = with pkgs; [
    # nix
    nixd
    nixfmt

    # one-off python scripts (uv fetches interpreters itself)
    uv
  ];
}
