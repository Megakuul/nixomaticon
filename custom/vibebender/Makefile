TAGS := desktop,production,webkit2_41

.PHONY: dev dev-fake run test check build

# Live-reloading window against a real SonicWall (pkexec asks on Connect).
dev:
	wails dev -tags webkit2_41

# UI work without root or VPN: simulated session that drops every 30s.
# Password "wrong" -> rejected login, username containing "otp" -> 2FA prompt.
dev-fake:
	VIBEBENDER_FAKE=1 VIBEBENDER_FAKE_DROP=30 wails dev -tags webkit2_41

run:
	go run -tags $(TAGS) .

test:
	go test -race -count=1 -tags $(TAGS) ./...

check: test
	test -z "$$(gofmt -l .)"
	go vet -tags $(TAGS) ./...
	nix flake check path:.

build:
	nix build path:.#vibebender
