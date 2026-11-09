require "nvchad.mappings"

-- add yours here

local map = vim.keymap.set

map("n", ";", ":", { desc = "CMD enter command mode" })
map("i", "jk", "<ESC>")
map("t", "<ESC>", "<C-\\><C-n>")
map("t", "jk", "<C-\\><C-n>")

-- kitty turns ctrl+hjkl into arrow keys, except while this user var is set
-- (see modules/term/kitty.nix). Then NvChad's <C-hjkl> switch splits; keep
-- the arrows for shells in :terminal.
if vim.env.KITTY_WINDOW_ID then
  local function in_editor(value)
    io.stdout:write("\27]1337;SetUserVar=in_editor=" .. vim.base64.encode(value) .. "\7")
  end
  local group = vim.api.nvim_create_augroup("kitty_in_editor", {})
  vim.api.nvim_create_autocmd({ "VimEnter", "VimResume" }, { group = group, callback = function() in_editor "1" end })
  vim.api.nvim_create_autocmd({ "VimLeavePre", "VimSuspend" }, { group = group, callback = function() in_editor "" end })
  if vim.v.vim_did_enter == 1 then
    in_editor "1"
  end

  map("t", "<C-h>", "<Left>")
  map("t", "<C-j>", "<Down>")
  map("t", "<C-k>", "<Up>")
  map("t", "<C-l>", "<Right>")
end

-- map({ "n", "i", "v" }, "<C-s>", "<cmd> w <cr>")

-- <leader>ff/<leader>fw respect .gitignore (fast); <leader>fa (NvChad) and
-- <leader>fW also search ignored and hidden files (node_modules, target, ...).
map("n", "<leader>fW", function()
  require("telescope.builtin").live_grep { additional_args = { "--no-ignore", "--hidden", "--glob=!.git/" } }
end, { desc = "telescope live grep all files" })

-- Local, dependency-free conflict inbox; preserves any existing leader mapping.
require("git_conflict_browser").setup()
