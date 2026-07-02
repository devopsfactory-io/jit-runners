RUNNER_VERSION ?= 2.332.0
JIT_RUNNERS_VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")

.PHONY: help test lint build clean lambda.build lambda.test ami.build ami.build-test ami.validate ami.prune image.build image.build-test image.validate image.build-distribute image.copy

help: ## Show this help
	@grep -E '^[a-zA-Z_.-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-24s\033[0m %s\n", $$1, $$2}'

test: lambda.test ## Run all tests

lint: ## Run linters
	cd lambda && golangci-lint run ./...

lambda.build: ## Build Lambda binaries (named bootstrap for provided.al2023 runtime)
	mkdir -p bin/webhook bin/scaleup bin/scaledown bin/lifecycle bin/rebalancer
	cd lambda && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../bin/webhook/bootstrap ./cmd/webhook
	cd lambda && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../bin/scaleup/bootstrap ./cmd/scaleup
	cd lambda && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../bin/scaledown/bootstrap ./cmd/scaledown
	cd lambda && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../bin/lifecycle/bootstrap ./cmd/lifecycle
	cd lambda && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../bin/rebalancer/bootstrap ./cmd/rebalancer

lambda.zip: lambda.build ## Build Lambda zips (bootstrap at root for provided.al2023)
	cd bin/webhook && zip -qj ../webhook.zip bootstrap
	cd bin/scaleup && zip -qj ../scaleup.zip bootstrap
	cd bin/scaledown && zip -qj ../scaledown.zip bootstrap
	cd bin/lifecycle && zip -qj ../lifecycle.zip bootstrap
	cd bin/rebalancer && zip -qj ../rebalancer.zip bootstrap

lambda.test: ## Run Lambda tests with coverage
	cd lambda && go test -coverprofile=coverage.out -covermode=atomic ./...
	cd lambda && go tool cover -func=coverage.out

lambda.vet: ## Run go vet on Lambda code
	cd lambda && go vet ./...

clean: ## Remove build artifacts
	rm -rf bin/ dist/
	rm -f lambda/coverage.out

check: lint lambda.vet lambda.test ## Run all checks (lint + vet + test)

check-fmt: ## Check Go formatting
	@test -z "$$(cd lambda && gofmt -l .)" || (echo "Files not formatted:" && cd lambda && gofmt -l . && exit 1)

ami.validate: ## Validate Packer template (AWS source)
	cd infra/packer && packer init . && packer validate -only=amazon-ebs.jit-runner .

ami.build: ## Build pre-baked private runner AMI with Packer (us-east-2 only)
	cd infra/packer && packer init . && packer build \
		-var "runner_version=$(RUNNER_VERSION)" \
		-var "jit_runners_version=$(JIT_RUNNERS_VERSION)" .

ami.build-test: ## Build a private test AMI with the jit-runner-pr prefix
	cd infra/packer && packer init . && packer build \
		-var "runner_version=$(RUNNER_VERSION)" \
		-var "jit_runners_version=$(JIT_RUNNERS_VERSION)" \
		-var 'ami_name_prefix=jit-runner-pr' .

ami.prune: ## Dry-run prune of stale private AMIs (us-east-2). Add APPLY=1 to apply.
	infra/scripts/ami-prune.sh --regions us-east-2 --stack-name jit-runners \
		--keep-latest 2 $(if $(filter 1,$(APPLY)),--apply,)

# ============================================================================
# GCE image build (mirrors ami.* targets — D9)
# ============================================================================

image.validate: ## Validate Packer template (GCP source)
	cd infra/packer && packer init . && packer validate -only=googlecompute.jit-runner \
		-var "gcp_project=placeholder" \
		.

image.build: ## Build pre-baked runner GCE image with Packer (multi-region US)
	@if [ -z "$(GCP_PROJECT)" ]; then echo "Usage: make image.build GCP_PROJECT=my-project"; exit 1; fi
	cd infra/packer && packer init . && packer build -only=googlecompute.jit-runner \
		-var "gcp_project=$(GCP_PROJECT)" \
		-var "jit_runners_version=$$(git describe --tags --always 2>/dev/null || echo dev)" \
		.

image.build-test: ## Build a private (single-region) test GCE image
	@if [ -z "$(GCP_PROJECT)" ]; then echo "Usage: make image.build-test GCP_PROJECT=my-project"; exit 1; fi
	cd infra/packer && packer init . && packer build -only=googlecompute.jit-runner \
		-var "gcp_project=$(GCP_PROJECT)" \
		-var "ami_name_prefix=jit-runner-pr" \
		-var "gcp_image_storage_locations=[\"us-central1\"]" \
		-var "jit_runners_version=$$(git describe --tags --always 2>/dev/null || echo dev)" \
		.

image.build-distribute: ## Build GCE image and replicate to US, EU, Asia multi-regions
	@if [ -z "$(GCP_PROJECT)" ]; then echo "Usage: make image.build-distribute GCP_PROJECT=my-project"; exit 1; fi
	cd infra/packer && packer init . && packer build -only=googlecompute.jit-runner \
		-var "gcp_project=$(GCP_PROJECT)" \
		-var "gcp_image_storage_locations=[\"us\", \"eu\", \"asia\"]" \
		-var "jit_runners_version=$$(git describe --tags --always 2>/dev/null || echo dev)" \
		.

image.copy: ## (NOTE) GCE images are multi-region by default via image_storage_locations.
	@echo "GCE image multi-region replication is a build-time setting on the GCE source"
	@echo "(image_storage_locations). Use:"
	@echo "  make image.build-distribute GCP_PROJECT=<project>"
	@echo "to publish a multi-region image. There is no post-build copy step on GCE."
	@false
