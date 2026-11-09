# Neovim plus every language server and formatter its config uses
# (config/nvim/lua/configs/{lspconfig,conform}.lua), always on PATH. Keep the
# lists in sync. Compilers/runtimes (go, cargo, ...) still come from dev flakes.
{pkgs, ...}: {
  home.packages = with pkgs; [
    neovim
    wl-clipboard
    fd
    ripgrep

    # nvim-treesitter compiles its parsers
    tree-sitter
    gcc

    # language servers (lspconfig.lua)
    lua-language-server # lua_ls
    vscode-langservers-extracted # html, cssls, eslint
    typescript # tsc (7.x, native `tsc --lsp`)
    svelte-language-server # svelte
    tailwindcss-language-server # tailwindcss
    templ # templ (lsp + formatter)
    gopls # gopls
    clang-tools # clangd + clang-format
    ocamlPackages.ocaml-lsp # ocamllsp
    yaml-language-server # yamlls
    rust-analyzer # rust_analyzer
    pyright # pyright
    bash-language-server # bashls
    buf # buf_ls + buf formatter

    # formatters (conform.lua)
    stylua
    prettierd
    gotools # goimports
    gofumpt
    ocamlPackages.ocamlformat
    alejandra
    yamlfmt
    rustfmt
    ruff
    shfmt
  ];

  xdg.configFile.nvim.source = ../../../config/nvim;
}
