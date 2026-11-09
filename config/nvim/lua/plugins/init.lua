return {

  {
    "stevearc/conform.nvim",
    event = "BufWritePre", -- uncomment for format on save
    opts = require "configs.conform",
  },

  {
    "neovim/nvim-lspconfig",
    config = function()
      require "configs.lspconfig"
    end,
  },

  {
    "megakuul/slopxd",
    ft = "nix",
    config = function()
      require("slopxd").setup {
        completion = false, -- nvim-cmp handles the completion UI
        keymaps = false, -- <C-Space> is already cmp.mapping.complete()
        completeopt = false,
      }
    end,
  },

  -- The main branch neither installs parsers from opts nor enables highlighting
  -- by itself: install them explicitly and start treesitter per buffer.
  {
    "nvim-treesitter/nvim-treesitter",
    branch = "main",
    lazy = false,
    build = ":TSUpdate",
    config = function()
      require("nvim-treesitter").install {
        "lua", "luadoc", "printf", "vim", "vimdoc", "query", "regex",
        "markdown", "markdown_inline", "json", "yaml", "toml", "nix", "bash",
        "go", "gomod", "gosum", "gowork", "templ",
        "javascript", "typescript", "tsx", "html", "css", "svelte",
        "c", "cpp", "rust", "python", "ocaml", "proto", "sql",
      }
      vim.api.nvim_create_autocmd("FileType", {
        callback = function(args)
          pcall(vim.treesitter.start, args.buf)
        end,
      })
    end,
  },

  {
    "stevearc/oil.nvim",
    dependencies = { "nvim-tree/nvim-web-devicons" },
    lazy = false,
    config = function()
      require("oil").setup {
        columns = { "icon" },
        keymaps = {
          ["<C-h>"] = false,
        },
        view_options = {
          show_hidden = true,
        },
      }
      vim.keymap.set("n", "-", "<CMD>Oil<CR>", { desc = "Open parent directory" })
    end,
  },

  {
    "ThePrimeagen/harpoon",
    branch = "harpoon2",
    dependencies = { "nvim-lua/plenary.nvim" },
  },

  {
    "ghillb/cybu.nvim",
    dependencies = { "nvim-lua/plenary.nvim", "nvim-tree/nvim-web-devicons" },
  },

  {
    "nvim-pack/nvim-spectre",
    dependencies = { "nvim-lua/plenary.nvim" },
  },

  {
    "folke/noice.nvim",
    event = "VeryLazy",
    opts = {
      routes = {
        {
          filter = { error = true },
          opts = { skip = true },
        },
      },
      lsp = {
        hover = {
          enabled = false,
        },
        signature = {
          enabled = false,
          auto_open = {
            enabled = false,
            trigger = false,
            luasnip = false,
          },
        },
        -- override markdown rendering so that **cmp** and other plugins use **Treesitter**
        override = {
          ["vim.lsp.util.convert_input_to_markdown_lines"] = true,
          ["vim.lsp.util.stylize_markdown"] = true,
          ["cmp.entry.get_documentation"] = true, -- requires hrsh7th/nvim-cmp
        },
      },
      presets = {
        command_palette = true,
        long_message_to_split = true,
        inc_rename = false,
      },
    },
    dependencies = {
      -- if you lazy-load any plugin below, make sure to add proper `module="..."` entries
      "MunifTanjim/nui.nvim",
      -- OPTIONAL:
      --   `nvim-notify` is only needed, if you want to use the notification view.
      --   If not available, we use `mini` as the fallback
      {
        "rcarriga/nvim-notify",
        -- The transparent theme has no background for NotifyBackground.
        opts = { background_colour = "#000000" },
      },
    },
  },

  {
    "lewis6991/gitsigns.nvim",
    dependencies = { "nvim-lua/plenary.nvim" },
  },

  {
    "folke/flash.nvim",
    event = "VeryLazy",
    opts = {},
    keys = {
      {
        "gw",
        mode = { "n", "x", "o" },
        function()
          require("flash").jump()
        end,
      },
    },
  },
}
