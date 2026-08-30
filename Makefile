# publix — build, test and development tasks.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build ui go test test-all lint fmt dev clean install

## build: compile the dashboard into the binary
build: ui go

## ui: build the Svelte dashboard into internal/api/dist
ui:
	cd web && npm ci --no-audit --no-fund && npm run build

## go: compile the binary (uses whatever dashboard is currently built)
go:
	go build -trimpath -ldflags "$(LDFLAGS)" -o publix ./cmd/publix

## test: unit tests only — no Docker daemon needed
test:
	go test -short ./...

## test-all: everything, including tests against a real Docker daemon
test-all:
	go test -timeout 30m ./...

## lint: vet and formatting check
lint:
	go vet ./...
	@unformatted=$$(gofmt -l . | grep -v '^web/' || true); \
	if [ -n "$$unformatted" ]; then echo "not gofmt'd:"; echo "$$unformatted"; exit 1; fi

fmt:
	gofmt -w $$(git ls-files '*.go')

## dev: run the server, with the dashboard served by Vite on :5173
dev:
	@echo "Server on :4321, dashboard on :5173 (open the Vite one)"
	@(cd web && npm run dev &) ; go run ./cmd/publix serve -v

## install: build and place the binary in /usr/local/bin
install: build
	install -m 0755 publix /usr/local/bin/publix

clean:
	rm -f publix
	rm -rf internal/api/dist/assets internal/api/dist/index.html
