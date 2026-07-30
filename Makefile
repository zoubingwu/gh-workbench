GOLANGCI_LINT_VERSION := v2.12.2
GOLANGCI_LINT := $(CURDIR)/.tools/golangci-lint/$(GOLANGCI_LINT_VERSION)/golangci-lint

.PHONY: build check dev install lint test

build:
	pnpm --dir web build
	go build ./cmd/gh-workbench

check: lint
	pnpm --dir web check
	go test -race ./...
	go vet ./...

dev:
	pnpm --dir web build
	go run ./cmd/gh-workbench

install:
	pnpm --dir web install --frozen-lockfile

$(GOLANGCI_LINT):
	curl -sSfL https://golangci-lint.run/install.sh | \
		sh -s -- -b $(dir $@) $(GOLANGCI_LINT_VERSION)

lint: $(GOLANGCI_LINT)
	pnpm --dir web build
	$(GOLANGCI_LINT) run ./...

test:
	pnpm --dir web test
	pnpm --dir web build
	go test ./...
