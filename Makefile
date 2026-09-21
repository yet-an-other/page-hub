.PHONY: build frontend test verify release clean

VERSION ?= dev
GOFLAGS ?=

build: frontend
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o dist/page-hub-$(VERSION)-linux-amd64 ./cmd/page-hub

frontend:
	pnpm --dir web install --frozen-lockfile
	pnpm --dir web build

test:
	go test ./...

verify:
	pnpm --dir web install --frozen-lockfile
	pnpm --dir web verify:openapi
	pnpm --dir web typecheck
	pnpm --dir web lint
	gofmt -w cmd internal web/embed.go
	pnpm --dir web build
	go test ./...
	go vet ./...
	pnpm --dir web exec playwright test -c playwright.config.ts

release: build
	sha256sum dist/page-hub-$(VERSION)-linux-amd64 > dist/page-hub-$(VERSION)-linux-amd64.sha256

clean:
	rm -rf dist
