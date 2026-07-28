.PHONY: build check dev install test

build:
	pnpm --dir web build
	go build ./cmd/gh-workbench

check:
	pnpm --dir web check
	go test -race ./...
	go vet ./...

dev:
	pnpm --dir web build
	go run ./cmd/gh-workbench

install:
	pnpm --dir web install --frozen-lockfile

test:
	pnpm --dir web test
	pnpm --dir web build
	go test ./...
