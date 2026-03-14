RUNNER_VERSION ?= 2.332.0
JIT_RUNNERS_VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")

AMI_DISTRIBUTION_REGIONS ?= us-east-1 us-west-1 us-west-2 eu-west-1 eu-west-2 eu-west-3 eu-central-1 eu-north-1 sa-east-1
SOURCE_REGION ?= us-east-2

.PHONY: help test lint build clean lambda.build lambda.test ami.build ami.build-test ami.validate ami.distribute ami.copy

help: ## Show this help
	@grep -E '^[a-zA-Z_.]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

test: lambda.test ## Run all tests

lint: ## Run linters
	cd lambda && golangci-lint run ./...

lambda.build: ## Build Lambda binaries (named bootstrap for provided.al2023 runtime)
	mkdir -p bin/webhook bin/scaleup bin/scaledown
	cd lambda && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../bin/webhook/bootstrap ./cmd/webhook
	cd lambda && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../bin/scaleup/bootstrap ./cmd/scaleup
	cd lambda && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../bin/scaledown/bootstrap ./cmd/scaledown

lambda.zip: lambda.build ## Build Lambda zips (bootstrap at root for provided.al2023)
	cd bin/webhook && zip -qj ../webhook.zip bootstrap
	cd bin/scaleup && zip -qj ../scaleup.zip bootstrap
	cd bin/scaledown && zip -qj ../scaledown.zip bootstrap

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

ami.validate: ## Validate Packer template
	cd infra/packer && packer init . && packer validate .

ami.build: ## Build pre-baked runner AMI with Packer
	cd infra/packer && packer init . && packer build \
		-var "runner_version=$(RUNNER_VERSION)" \
		-var "jit_runners_version=$(JIT_RUNNERS_VERSION)" .

ami.build-test: ## Build a private (non-public) test AMI
	cd infra/packer && packer init . && packer build \
		-var "runner_version=$(RUNNER_VERSION)" \
		-var "jit_runners_version=$(JIT_RUNNERS_VERSION)" \
		-var 'ami_groups=[]' .

ami.build-distribute: ## Build AMI and copy to all distribution regions
	cd infra/packer && packer init . && packer build \
		-var "runner_version=$(RUNNER_VERSION)" \
		-var "jit_runners_version=$(JIT_RUNNERS_VERSION)" \
		-var 'ami_regions=["us-east-1","us-west-1","us-west-2","eu-west-1","eu-west-2","eu-west-3","eu-central-1","eu-north-1","sa-east-1"]' .

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
