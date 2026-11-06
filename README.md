# Nixomaticon ❄️

[beastos](https://github.com/megakuul/beastos)' home-manager setup, running on Fedora Workstation. Home-manager owns the desktop (Hyprland, kiwi-shell, theming), all apps and toolchains, and the in-house projects in `custom/`. Fedora keeps the kernel, drivers, LUKS, systemd services and a few packages listed in `bootstrap.sh`.

| Path | What |
| --- | --- |
| `profile.nix` | your username, git identity, teleport proxy |
| `modules/` | beastos home modules, `fedora.nix` (non-NixOS glue), `informaticon/` (twinkle, voiper, vibebender) |
| `custom/voiper`, `custom/vibebender` | full git clones, built straight from the working tree on every switch |
| `bootstrap.sh` | one-time root setup |

## 1. Install Fedora (with LUKS)

1. Download **Fedora Workstation** (x86_64) from fedoraproject.org and write it to a USB stick:
   `sudo dd if=Fedora-Workstation-Live-*.iso of=/dev/sdX bs=4M status=progress oflag=sync` (or use Fedora Media Writer).
2. Boot the stick in UEFI mode (Secure Boot can stay on) and pick **Install Fedora**.
3. **Language / keyboard**: English (US), keyboard *Swiss German*.
4. **Installation method**: choose the target disk, then **Use entire disk** (wipes it).
5. **Storage / encryption**: tick **Encrypt my data** and set a strong passphrase. This gives LUKS2 with btrfs inside, and you'll type the passphrase at every boot.
6. Review and click **Install**, then reboot and remove the stick.
7. **First-boot setup**: set the timezone, then create the user **`linus`** (it must match `profile.nix`). The first user is an administrator (wheel).
8. Bring Fedora up to date: `sudo dnf upgrade --refresh -y && sudo reboot`

## 2. Nix + this flake

```sh
curl -fsSL https://install.determinate.systems/nix | sh -s -- install --no-confirm   # SELinux-aware
exec bash -l                                                 # pick up nix
mkdir -p ~/Documents/repos && cd ~/Documents/repos
git clone --recurse-submodules <your nixomaticon remote> nixomaticon   # or copy the folder over
cd nixomaticon && ./bootstrap.sh
sudo reboot
```

After the reboot: LUKS passphrase → GDM login (pick **Hyprland (nix)** at the gear icon once; GDM remembers it). **SUPER+G** locks with Fedora's swaylock.

`bootstrap.sh` does all of this:
- installs swaylock, docker, libvirt, swtpm and waydroid with dnf, and adds you to `docker,libvirt,input`
- runs the first `home-manager switch`, then `non-nixos-gpu-setup` (GPU drivers for nix programs)
- sets fish as your login shell
- adds the `Hyprland (nix)` session to GDM

## 3. Git keys

Generate the keys git and ssh expect, in `~/.config/git`:
```sh
cd ~/.config/git && ssh-keygen -f signingkey && ssh-keygen -f github && ssh-keygen -f codeberg
```

## Upgrade everything

```sh
make upgrade    # dnf + COPR, flatpaks, firmware, Nix itself, fresh pins for all inputs, switch
make update     # only nix side: nix flake update + switch (nvd shows the diff)
make switch     # apply local changes (also rebuilds custom/* from your working tree)
```

- A new Fedora release: `sudo dnf system-upgrade download --releasever=<N+1> && sudo dnf system-upgrade reboot`.
- If home-manager warns about GPU drivers: `sudo $(readlink -f ~/.nix-profile/bin/non-nixos-gpu-setup)`.
- Hyprland follows kiwi-shell's own nixpkgs, because kiwi's Hyprland plugin must match the compositor.
- voiper keeps its own `flake.lock` (vendor and pnpm hashes): update it in `custom/voiper` with `nix flake update` when you want it.
- If an update breaks a package, remove it from `modules/packages/*` (that's how penpot and frog left).

## Develop voiper / vibebender

Both use direnv and `nix develop`. Changes go live on the next `make switch`, no commit needed.

```sh
make dev-voiper        # wails dev with live reload
make dev-vibebender    # vibebender UI against a simulated VPN (no root; drops every 30s)
make test              # both test suites
make check             # tests + vet + nix flake check for both
```
Inside each project, `make` targets and `AGENTS.md` describe the details (e.g. `make dev` = vibebender against the real SonicWall).

To put this repo in git, keep the projects as submodules:
`git init && git submodule add git@github.com:megakuul/voiper custom/voiper && git submodule add <your vibebender fork> custom/vibebender && git add -A`
The flake sets `self.submodules = true` so their files are included.
