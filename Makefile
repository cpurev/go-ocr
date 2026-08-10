BINARY := bin/api

.PHONY: help run build fmt vet tidy check clean

help: ## Show this help
	@grep -E '^[a-z-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

run: ## Start the API server (Ctrl+C for graceful shutdown)
	go run ./cmd/api

build: ## Compile the binary into ./bin
	go build -o $(BINARY) ./cmd/api

fmt: ## Format all Go files
	go fmt ./...

vet: ## Run the built-in static analyzer
	go vet ./...

tidy: ## Sync go.mod/go.sum with the imports actually used
	go mod tidy

check: fmt vet build ## Format, vet, and compile

clean: ## Remove build artifacts
	rm -rf bin
