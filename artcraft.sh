#!/usr/bin/env bash
# Installs or upgrades the ArtCraft suite (https://github.com/storytold) as
# Fedora rpms: the Adobe-style apps, the office apps and the ArtCraft Launcher.
# Run by bootstrap.sh and `make upgrade`; safe to re-run (dnf skips what is current).
set -euo pipefail

apps=(
  # Adobe suite
  photocraft  # Photoshop
  vectorcraft # Illustrator
  filmcraft   # Premiere Pro
  lightcraft  # Lightroom
  effectcraft # After Effects
  designcraft # InDesign
  pdfcraft    # Acrobat
  # Office
  wordcraft  # Word
  gridcraft  # Excel
  deckcraft  # PowerPoint
  # installs/updates the above from the GUI too
  craft-launcher
)

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

rpms=()
for app in "${apps[@]}"; do
  # name, url and sha256 of the release's x86_64 rpm
  read -r name url sha < <(
    curl -fsSL "https://api.github.com/repos/storytold/$app/releases/latest" | python3 -c '
import json, sys
for a in json.load(sys.stdin)["assets"]:
    if a["name"].endswith("-linux-x86_64.rpm"):
        print(a["name"], a["browser_download_url"], (a.get("digest") or "").removeprefix("sha256:"))
        break
'
  )
  if [ -z "${url:-}" ]; then
    echo "artcraft: no x86_64 rpm in the latest $app release, skipping" >&2
    continue
  fi
  curl -fsSL -o "$tmp/$name" "$url"
  if [ -n "$sha" ]; then
    echo "$sha  $tmp/$name" | sha256sum -c --quiet -
  else
    echo "artcraft: $name has no published sha256, installing unverified" >&2
  fi
  rpms+=("$tmp/$name")
done

# install new apps and upgrade installed ones in one transaction
[ ${#rpms[@]} -eq 0 ] || sudo dnf install -y "${rpms[@]}"
