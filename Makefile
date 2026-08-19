GOBIN   ?= $(shell go env GOBIN)
BINDIR  ?= $(if $(strip $(GOBIN)),$(GOBIN),$(HOME)/.local/bin)

.PHONY: build install test fmt fmt-check run

build:
	go build -o bin/rig ./cmd/rig

install: build
	mkdir -p $(BINDIR)
	install -m 0755 bin/rig $(BINDIR)/

test:
	go vet ./...
	go test ./...

fmt:
	gofmt -w .

fmt-check:
	@if [ -n "$$(gofmt -l .)" ]; then \
		echo "make fmt-check: unformatted files (run 'make fmt'):" >&2; \
		gofmt -l . >&2; \
		exit 1; \
	fi

run: build
	./bin/rig
