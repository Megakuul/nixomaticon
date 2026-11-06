#!/usr/bin/env bash
# One-time setup of a fresh Fedora Workstation for this flake. Safe to re-run.
# Everything else is home-manager (`make switch`).
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")"
user=$(id -un)
profile=$HOME/.nix-profile

# --- the only dnf packages: things that need system services / PAM ----------
sudo dnf install -y swaylock moby-engine libvirt-daemon-kvm libvirt-daemon-config-network swtpm waydroid
sudo systemctl enable --now docker libvirtd
sudo usermod -aG docker,libvirt,input "$user"

# --- first home-manager switch -------------------------------------------------
nix run .#home-manager -- switch --flake ".#$user" -b backup

# --- GPU drivers for nix programs (Hyprland etc.) -------------------------------
sudo "$(readlink -f "$profile/bin/non-nixos-gpu-setup")"

# --- login shell stays Fedora's bash; terminals start fish themselves ----------
# (a /nix shell as login shell breaks TTY login when nix is unavailable)
sudo usermod -s /bin/bash "$user"
sudo sed -i "\|^$profile/bin/fish\$|d" /etc/shells

# --- Hyprland session for GDM ----------------------------------------------------
sudo tee /usr/share/wayland-sessions/hyprland-nix.desktop >/dev/null <<EOF
[Desktop Entry]
Name=Hyprland (nix)
Comment=Hyprland from home-manager
Exec=$profile/bin/hyprland-session
Type=Application
DesktopNames=Hyprland
EOF

echo "Done. Reboot, pick 'Hyprland (nix)' at the GDM gear icon (once) and log in. SUPER+G locks."
