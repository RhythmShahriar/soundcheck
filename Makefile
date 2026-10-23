GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

.PHONY: build test lint fmt check hooks bench

build: ## build bin/rhythm-soundcheck
	go build -o bin/rhythm-soundcheck ./cmd/rhythm-soundcheck

test: ## run tests (browser tests need Chrome)
	go test -race ./...

lint:
	$(GOLANGCI) run ./...

fmt:
	gofmt -w .

check: lint test ## everything CI runs

hooks: ## enable the Conventional Commits hook
	git config core.hooksPath .githooks

bench: ## run the broken-sites benchmark (needs Docker); writes docs/benchmark.md
	go run ./testbed/bench
