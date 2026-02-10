.PHONY: test test-all coverage clean build cross install lint

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -X github.com/newtron-network/newtron/pkg/version.Version=$(VERSION) \
           -X github.com/newtron-network/newtron/pkg/version.GitCommit=$(GIT_COMMIT)

BINARIES := newtron newtlab newtest newtlink
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

# Default test (unit tests only, no build tags)
test:
	go test ./...

# Run all tests
test-all: test

# Coverage report
coverage:
	@echo "Running coverage analysis..."
	go test -coverprofile=coverage.out -count=1 ./...
	go tool cover -func=coverage.out | tail -1

# Coverage HTML report
coverage-html: coverage
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Build all binaries for current platform
build:
	@mkdir -p bin
	@for b in $(BINARIES); do \
		echo "Building $$b..."; \
		go build -ldflags "$(LDFLAGS)" -o bin/$$b ./cmd/$$b; \
	done
	@echo "Binaries in bin/"

# Cross-compile all binaries for all platforms
cross:
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		echo "Building $$os/$$arch..."; \
		mkdir -p bin/$$os-$$arch; \
		for b in $(BINARIES); do \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" \
				-o bin/$$os-$$arch/$$b ./cmd/$$b; \
		done; \
	done
	@echo "Cross-compiled binaries in bin/<os>-<arch>/"

# Copy newtlink variants alongside newtlab for auto-upload
install: cross
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		cp bin/$$os-$$arch/newtlink bin/newtlink-$$os-$$arch; \
	done
	@echo "Installed newtlink-<os>-<arch> binaries in bin/"

# Lint
lint:
	golangci-lint run ./...

# Clean
clean:
	rm -rf bin
	rm -f coverage.out coverage.html
	go clean -testcache
