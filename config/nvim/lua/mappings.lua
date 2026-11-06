require "nvchad.mappings"

-- add yours here

local map = vim.keymap.set

map("n", ";", ":", { desc = "CMD enter command mode" })
map("i", "jk", "<ESC>")
map("t", "<ESC>", "<C-\\><C-n>")
map("t", "jk", "<C-\\><C-n>")

-- map({ "n", "i", "v" }, "<C-s>", "<cmd> w <cr>")

-- <leader>ff/<leader>fw respect .gitignore (fast); <leader>fa (NvChad) and
-- <leader>fW also search ignored and hidden files (node_modules, target, ...).
map("n", "<leader>fW", function()
  require("telescope.builtin").live_grep { additional_args = { "--no-ignore", "--hidden", "--glob=!.git/" } }
end, { desc = "telescope live grep all files" })

-- Local, dependency-free conflict inbox; preserves any existing leader mapping.
require("git_conflict_browser").setup()
