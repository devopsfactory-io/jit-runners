RUNNER_VERSION ?= 2.332.0
JIT_RUNNERS_VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")

AMI_DISTRIBUTION_REGIONS ?= us-east-1
SOURCE_REGION ?= us-east-2

.PHONY: help test lint build clean lambda.build lambda.test ami.build ami.build-test ami.validate ami.build-distribute ami.copy ami.prune image.build image.build-test image.validate image.build-distribute image.copy

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

ami.build: ## Build pre-baked runner AMI with Packer
	cd infra/packer && packer init . && packer build \
		-var "runner_version=$(RUNNER_VERSION)" \
		-var "jit_runners_version=$(JIT_RUNNERS_VERSION)" .

ami.build-test: ## Build a private (non-public) test AMI
	cd infra/packer && packer init . && packer build \
		-var "runner_version=$(RUNNER_VERSION)" \
		-var "jit_runners_version=$(JIT_RUNNERS_VERSION)" \
		-var 'ami_groups=[]' .

ami.build-distribute: ## Build AMI and copy to the distribution region (us-east-1)
	cd infra/packer && packer init . && packer build \
		-var "runner_version=$(RUNNER_VERSION)" \
		-var "jit_runners_version=$(JIT_RUNNERS_VERSION)" \
		-var 'ami_regions=["us-east-1"]' .

ami.copy: ## Copy an existing AMI to all distribution regions (requires AMI_ID)
	@if [ -z "$(AMI_ID)" ]; then echo "Usage: make ami.copy AMI_ID=ami-xxxxx"; exit 1; fi
	@echo "Disabling block public access for AMIs in target regions..."
	@for region in $(AMI_DISTRIBUTION_REGIONS); do \
		aws ec2 disable-image-block-public-access --region $${region} > /dev/null 2>&1 || true; \
	done
	@AMI_NAME=$$(aws ec2 describe-images --image-ids $(AMI_ID) --region $(SOURCE_REGION) --query 'Images[0].Name' --output text); \
	for region in $(AMI_DISTRIBUTION_REGIONS); do \
		echo "Copying $(AMI_ID) to $${region}..."; \
		NEW_AMI=$$(aws ec2 copy-image \
			--source-region $(SOURCE_REGION) \
			--source-image-id $(AMI_ID) \
			--region $${region} \
			--name "$${AMI_NAME}" \
			--description "jit-runner pre-baked AMI" \
			--query 'ImageId' --output text); \
		echo "  -> $${NEW_AMI} ($${region})"; \
		echo "  Making public..."; \
		aws ec2 wait image-available --image-ids $${NEW_AMI} --region $${region}; \
		aws ec2 modify-image-attribute --image-id $${NEW_AMI} --region $${region} --launch-permission "Add=[{Group=all}]"; \
	done
	@echo "Done. AMI distributed to all regions."

ami.prune: ## Dry-run prune of stale public AMIs (us-east-1,us-east-2). Add APPLY=1 to apply.
	infra/scripts/ami-prune.sh --regions us-east-1,us-east-2 --stack-name jit-runners \
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
