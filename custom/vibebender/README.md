# Vibebender

Slop fork in Go from [https://github.com/abrasive/nxBender](https://github.com/abrasive/nxBender), now with a tiny GUI.

You should probably not use this; I simply slopped it so that I can debug issues in Go. This thing just has to work as long as I have to use SonicWall.

## How it works

- The window (Wails + WebKitGTK, static `frontend/`) runs as your user and remembers the profile (never the password) in `~/.config/vibebender/config.json`.
- **Connect** runs `pkexec vibebender helper`. The root helper does login, 2FA, pppd, routes and DNS, and streams JSON events back over stdout. The password only travels over its stdin.
- **Auto-reconnect** (on by default): if the tunnel drops, the helper reconnects with backoff (1s, 2s, 4s … 60s) without asking for root again. A rejected login is never retried.
- Closing the window (or **Disconnect**) tears the tunnel down; the helper also exits when the GUI goes away.
- You need a polkit agent in your session (e.g. `hyprpolkitagent`).

## Develop

```sh
direnv allow          # or: nix develop
make dev-fake         # live-reload UI with a simulated VPN (no root, drops every 30s)
make dev              # live-reload against a real SonicWall
make test             # protocol tests against the fake session
make check            # tests + gofmt + vet + nix flake check
nix run .#vibebender
```

> [!WARNING]
> Don't do AI kids
