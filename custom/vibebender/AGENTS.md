# Vibebender — SonicWall NetExtender client

A Go port of nxBender with a small Wails window. Keep it small: one Go package, a static `frontend/` (no npm, no build step).

## Architecture

- `main.go`: GUI entry; `vibebender helper [--fake]` is the root helper.
- `app.go`: GUI side. Saves the profile, spawns `pkexec <self> helper`, relays helper events to the UI as `vb:event`.
- `helper.go`: root side. JSON lines over stdin/stdout (`command` in, `event` out), auto-reconnect loop with backoff. Stdin EOF means disconnect.
- `session.go`, `tunnel.go`, `ppp.go`, `routes.go`, `dns.go`: the SonicWall protocol, pppd, routes and DNS. `authError` marks a rejected login, which must never be retried (account lockout).
- `fake.go`: simulated session for UI work and tests.
- `frontend/`: `index.html`, `style.css`, `app.js`, calling `window.go.main.App.*`.

## Rules

- The password only travels over the helper's stdin; never argv, env or disk.
- Anything that needs root belongs in the helper; the GUI never touches pppd, routes or DNS.
- pppd is pinned at build time (`-X main.pppdPath`), because pkexec resets PATH.
- Read surrounding code first, keep functions focused, comment decisions rather than the obvious.

## Verification

- `make test`: protocol tests drive `serveHelper` against the fake session (connect, 2FA, drop and reconnect, rejected login, GUI exit).
- `make check`: tests + gofmt + vet + `nix flake check` (the nix build runs the tests too).
- `make dev-fake` for UI changes; real SonicWall behavior needs a manual test with `make dev`. Say which you did.
