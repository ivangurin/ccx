BIN_DIR=./bin
PREFIX ?= /opt/homebrew

GREEN := \033[32m
RED := \033[31m
RESET := \033[0m

.PHONY: run
run: .hooks ## Run the daemon in foreground (dev)
	@echo "$(GREEN)▶$(RESET) Running ccx daemon..."
	@go run ./cmd/ccx serve

.PHONY: build
build: ## Build the binary
	go build -o $(BIN_DIR)/ccx ./cmd/ccx

.PHONY: install
install: build ## Install the binary to $(PREFIX)/bin/ccx
	install -m 0755 $(BIN_DIR)/ccx $(PREFIX)/bin/ccx

.PHONY: lint
lint: .hooks ## Run linters on changed files
	@echo "$(GREEN)▶$(RESET) Running linters on changed files..."
	@go tool golangci-lint run \
		--fix \
		--new-from-rev=main \
		--config=.golangci.yaml \
		--max-issues-per-linter=1000 \
		--max-same-issues=1000 \
		./...

.PHONY: lint-full
lint-full: ## Run linters on all files
	@echo "$(GREEN)▶$(RESET) Running linters on all files..."
	@go tool golangci-lint run \
		--config=.golangci.yaml \
		--max-issues-per-linter=1000 \
		--max-same-issues=1000 \
		./...

.PHONY: hooks
hooks: ## Activate git hooks (pre-commit lint gate)
	@echo "$(GREEN)▶$(RESET) Activating git hooks..."
	@git config core.hooksPath .githooks
	@echo "$(GREEN)✔$(RESET) pre-commit hook active: golangci-lint blocks commits with issues"

# idempotent auto-activation: hook works for anyone running the common targets,
# without a separate `make hooks` step (core.hooksPath is local, not committable)
.PHONY: .hooks
.hooks:
	@git config core.hooksPath .githooks

.PHONY: clean-mocks
clean-mocks: ## Clean old mocks
	@echo "$(RED)▶$(RESET) Cleaning old mocks..."
	@rm -rf $$(find . -type d -name mocks)

.PHONY: gen-mocks
gen-mocks: clean-mocks ## Generate mocks
	@echo "$(GREEN)▶$(RESET) Generating mocks..."
	@go tool mockery --log-level="error" || (echo "$(RED)✖\033[0m Mockery failed" && exit 1)

.PHONY: format
format:
	@echo "$(GREEN)▶$(RESET) Running code formatting..."
	@go tool gofumpt -l -w -extra .

.PHONY: fix
fix:
	@echo "$(GREEN)▶$(RESET) Running code fixing..."
	@go fix ./...

.PHONY: test
test: .hooks ## Run tests with coverage
	@echo "$(GREEN)▶$(RESET) Running tests with coverage..."
	@go test -v -count=1 ./... -coverprofile coverage.out.tmp -coverpkg=./...
	@grep -vE "mock.go|pb.go" coverage.out.tmp > coverage.out
	@rm -f coverage.out.tmp
	@go tool cover -func coverage.out | grep total: | awk '{print "Test coverage percent: " $$3}'
	@rm -f coverage.out

.PHONY: generate
generate: .hooks
	@echo "$(GREEN)▶$(RESET) Code generation..."
	@$(MAKE) gen-mocks fix format
	@echo "$(GREEN)▶$(RESET) Code generation completed successfully"

.PHONY: deadcode
deadcode:
	@echo "$(GREEN)▶$(RESET) Finding dead code..."
	@go tool deadcode -test ./...
