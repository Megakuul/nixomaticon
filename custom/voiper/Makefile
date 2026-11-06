.PHONY: dev-voiper build-voiper frontend test check

frontend:
	pnpm --dir web install --frozen-lockfile
	pnpm --dir web build

dev-voiper: frontend
	cd cmd/voiper && wails dev -tags "webkit2_41,opus,speex,secretservice"

build-voiper:
	nix build path:.#voiper

test:
	go test -race -timeout=3m -tags "webkit2_41,opus,speex,secretservice" ./pkg/... ./internal/... ./cmd/...

check: frontend
	go vet -tags "webkit2_41,opus,speex,secretservice" ./...
	nix flake check path:.
