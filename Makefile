GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

.PHONY: build test test-short fuzz lint fmt check hooks testbed testbed-down bench

build: ## build bin/rhythm-soundcheck
	go build -o bin/rhythm-soundcheck ./cmd/rhythm-soundcheck

# The visual checks take about 6 minutes under -race, so Go's 10 minute
# default timeout is too close for the whole suite.
test: ## run tests (browser tests need Chrome)
	go test -race -timeout 30m ./...

test-short: ## quick pass: tests that honour -short skip slow browser work
	go test -short -race -timeout 10m ./...

fuzz: ## run every fuzz target for FUZZTIME each (default 30s)
	scripts/fuzz.sh

lint:
	$(GOLANGCI) run ./...

fmt:
	gofmt -w .

check: lint test ## everything CI runs

hooks: ## enable the Conventional Commits hook
	git config core.hooksPath .githooks

testbed: ## start the 20 broken sites in Docker (127.0.0.1:18800 and :18810)
	$(MAKE) -C testbed up

testbed-down:
	$(MAKE) -C testbed down

bench: build ## run the broken-sites benchmark against the testbed; writes docs/benchmark.md
	go run -tags testbed ./testbed/bench -bin $(CURDIR)/bin/rhythm-soundcheck
