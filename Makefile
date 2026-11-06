.PHONY: switch gpu upgrade update check dev-voiper dev-vibebender test

# Apply the config (packages, dotfiles, desktop, custom/* builds).
switch:
	nh home switch $(CURDIR)
	@$(MAKE) --no-print-directory gpu

# Point /run/opengl-driver at this config's Mesa (needs sudo, only when it changed).
gpu:
	@setup=$$(readlink -f $(HOME)/.nix-profile/bin/non-nixos-gpu-setup); \
	want=$$(grep -o '/nix/store/[^ ]*' $$(dirname $$setup)/../lib/tmpfiles.d/non-nixos-gpu.conf | tail -1); \
	if [ "$$(readlink /run/opengl-driver)" = "$$want" ]; then echo "GPU drivers up to date"; \
	else echo "Updating GPU drivers (sudo)"; sudo $$setup; fi

# Everything: Fedora packages, flatpaks, firmware, Nix itself, all flake inputs.
upgrade:
	sudo dnf upgrade --refresh -y
	flatpak update -y
	-fwupdmgr refresh --force && fwupdmgr update -y
	sudo determinate-nixd upgrade
	$(MAKE) update

# Fresh pins for every input, then switch (shows a package diff via nvd).
update:
	nix flake update --flake $(CURDIR)
	$(MAKE) switch

check:
	nix flake check path:$(CURDIR)
	cd custom/vibebender && nix develop path:. -c make check
	cd custom/voiper && nix develop path:. -c make check

test:
	cd custom/vibebender && nix develop path:. -c make test
	cd custom/voiper && nix develop path:. -c make test

dev-voiper:
	cd custom/voiper && nix develop path:. -c make dev-voiper

dev-vibebender:
	cd custom/vibebender && nix develop path:. -c make dev-fake
