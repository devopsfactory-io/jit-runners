.PHONY: help test lint build clean lambda.build lambda.test

help: ## Show this help
	@grep -E '^[a-zA-Z_.]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

test: lambda.test ## Run all tests

lint: ## Run linters
	cd lambda && golangci-lint run ./...

lambda.build: ## Build Lambda binaries
	cd lambda && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../bin/webhook ./cmd/webhook
	cd lambda && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../bin/scaleup ./cmd/scaleup
	cd lambda && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../bin/scaledown ./cmd/scaledown

lambda.test: ## Run Lambda tests with coverage
	cd lambda && go test -race -coverprofile=coverage.out -covermode=atomic ./...
	cd lambda && go tool cover -func=coverage.out

lambda.vet: ## Run go vet on Lambda code
	cd lambda && go vet ./...

clean: ## Remove build artifacts
	rm -rf bin/ dist/
	rm -f lambda/coverage.out

check: lint lambda.vet lambda.test ## Run all checks (lint + vet + test)

check-fmt: ## Check Go formatting
	@test -z "$$(cd lambda && gofmt -l .)" || (echo "Files not formatted:" && cd lambda && gofmt -l . && exit 1)
