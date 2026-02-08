.PHONY: test test-all coverage clean build lint

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

# Build
build:
	go build ./cmd/...

# Lint
lint:
	golangci-lint run ./...

# Clean
clean:
	rm -f coverage.out coverage.html
	go clean -testcache
