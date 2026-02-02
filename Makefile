.PHONY: test test-integration test-e2e test-e2e-full test-all coverage clean
.PHONY: redis-start redis-stop redis-seed redis-ip status
.PHONY: labgen lab-start lab-stop lab-status

# Default test (unit tests only, no build tags)
test:
	go test ./...

# Integration tests: start Redis, seed, run tests, stop Redis
test-integration: redis-start redis-seed
	@echo "Running integration tests..."
	go test -tags integration -v -count=1 -p 1 ./...; rc=$$?; \
	$(MAKE) redis-stop; exit $$rc

# E2E tests (requires running containerlab topology)
# Results are saved to testlab/.generated/e2e-results.txt
test-e2e: ## Run e2e tests (requires running lab)
	@mkdir -p testlab/.generated
	set -o pipefail; \
	go test -tags e2e -v -count=1 -timeout 10m -p 1 ./... 2>&1 | tee testlab/.generated/e2e-results.txt

# Full E2E lifecycle: start lab, run tests, stop lab
test-e2e-full: lab-start ## Full lifecycle: start lab, run tests, stop lab
	@mkdir -p testlab/.generated
	set -o pipefail; \
	go test -tags e2e -v -count=1 -timeout 10m -p 1 ./... 2>&1 | tee testlab/.generated/e2e-results.txt; \
	rc=$$?; $(MAKE) lab-stop; exit $$rc

# Run all tests
test-all: test test-integration

# Coverage report (integration tests)
coverage: redis-start redis-seed
	@echo "Running coverage analysis..."
	go test -tags integration -coverprofile=coverage.out -count=1 -p 1 ./...; rc=$$?; \
	$(MAKE) redis-stop; \
	go tool cover -func=coverage.out | tail -1; exit $$rc

# Coverage HTML report
coverage-html: coverage
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Redis management (delegates to setup.sh)
redis-start:
	@./testlab/setup.sh redis-start

redis-stop:
	@./testlab/setup.sh redis-stop

redis-seed:
	@./testlab/setup.sh redis-seed

redis-ip:
	@./testlab/setup.sh redis-ip

status:
	@./testlab/setup.sh status

# Containerlab management
labgen:
	go build -o testlab/.generated/labgen ./cmd/labgen/

lab-start: labgen
	@./testlab/setup.sh lab-start $(or $(TOPO),spine-leaf)

lab-stop:
	@./testlab/setup.sh lab-stop

lab-status:
	@./testlab/setup.sh lab-status

# Build
build:
	go build ./cmd/...

# Lint
lint:
	golangci-lint run ./...

# Clean
clean:
	rm -f coverage.out coverage.html
	rm -rf testlab/.generated
	go clean -testcache
