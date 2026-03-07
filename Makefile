.PHONY: test test-all coverage clean build cross install lint tools

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -X github.com/newtron-network/newtron/pkg/version.Version=$(VERSION) \
           -X github.com/newtron-network/newtron/pkg/version.GitCommit=$(GIT_COMMIT)

BINARIES := newtron newtron-server newtlab newtrun newtlink
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

# Install diagram tools (graph-easy for DOT → ASCII rendering)
tools:
	@echo "Installing graph-easy..."
	@PERL_MM_OPT="INSTALL_BASE=$$HOME/perl5" \
		PERL_MB_OPT="--install_base $$HOME/perl5" \
		perl -MCPAN -e 'CPAN::Shell->notest("install", "Graph::Easy")' 2>&1 | tail -3
	@echo ""
	@echo "graph-easy installed to ~/perl5/bin/graph-easy"
	@echo "Usage: PERL5LIB=~/perl5/lib/perl5 ~/perl5/bin/graph-easy --from=dot --boxart < file.dot"
	@echo ""
	@echo "Or add to your shell profile:"
	@echo '  export PERL5LIB=$$HOME/perl5/lib/perl5'
	@echo '  export PATH=$$HOME/perl5/bin:$$PATH'

# Lint
lint:
	golangci-lint run ./...

# Clean
clean:
	rm -rf bin
	rm -f coverage.out coverage.html
	go clean -testcache
