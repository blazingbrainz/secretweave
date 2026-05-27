.PHONY: help versions build test lint tidy docker-build docker-push helm-package helm-publish publish clean

# Color output
RED    := \033[0;31m
GREEN  := \033[0;32m
YELLOW := \033[0;33m
NC     := \033[0m # No Color

# GitHub Container Registry
REGISTRY  := ghcr.io
ORG       := blazingbrainz
APP_NAME  := secretweave
HELM_REPO := helm-charts

# Extract versions from helm/Chart.yaml
CHART_FILE    := helm/Chart.yaml
APP_VERSION   := $(shell grep "^appVersion:" $(CHART_FILE) | sed 's/appVersion: "\(.*\)"/\1/')
CHART_VERSION := $(shell grep "^version:" $(CHART_FILE) | sed 's/version: \(.*\)/\1/')

# Build variables
BINARY       := bin/$(APP_NAME)
DOCKER_IMAGE := $(REGISTRY)/$(ORG)/$(APP_NAME):$(APP_VERSION)
HELM_CHART   := $(APP_NAME)-$(CHART_VERSION).tgz
OCI_ARTIFACT := $(REGISTRY)/$(ORG)/$(HELM_REPO)/$(APP_NAME):$(CHART_VERSION)

# Credentials — loaded in order: .env file → shell environment → make arguments
-include .env
export
GITHUB_USERNAME ?=
GITHUB_PAT      ?=

# Find oras executable (override with ORAS_PATH)
ORAS_PATH ?= $(shell command -v oras 2>/dev/null || echo "$$HOME/bin/oras")
ORAS      := $(ORAS_PATH)

debug:
	@echo "$(GREEN)Debug Information:$(NC)"
	@echo "  GITHUB_USERNAME: $(if $(GITHUB_USERNAME),✓ Set to '$(GITHUB_USERNAME)',✗ NOT SET)"
	@echo "  GITHUB_PAT:      $(if $(GITHUB_PAT),✓ Set (hidden for security),✗ NOT SET)"
	@echo "  ORAS Path:       $(if $(shell [ -x "$(ORAS)" ] && echo "yes"),✓ Found at $(ORAS),✗ NOT FOUND at $(ORAS))"
	@echo ""
	@echo "$(YELLOW)Troubleshooting:$(NC)"
	@if [ -z "$(GITHUB_USERNAME)" ]; then \
		echo "  - GITHUB_USERNAME not set!"; \
		echo "    Add to .env: GITHUB_USERNAME=your-username"; \
		echo "    Or export:   export GITHUB_USERNAME=your-username"; \
	fi
	@if [ -z "$(GITHUB_PAT)" ]; then \
		echo "  - GITHUB_PAT not set!"; \
		echo "    Add to .env: GITHUB_PAT=ghp_xxxx"; \
		echo "    Or export:   export GITHUB_PAT=ghp_xxxx"; \
	fi
	@if [ ! -x "$(ORAS)" ]; then \
		echo "  - oras not found at $(ORAS)"; \
		echo "    Try: which oras"; \
		echo "    Or:  make debug ORAS_PATH=/path/to/oras"; \
	fi
	@if [ -n "$(GITHUB_USERNAME)" ] && [ -n "$(GITHUB_PAT)" ] && [ -x "$(ORAS)" ]; then \
		echo "  ✓ All checks passed! Ready to publish."; \
	fi
	@echo ""

help:
	@echo "$(GREEN)SecretWeave Build & Publish Makefile$(NC)"
	@echo ""
	@echo "$(YELLOW)Usage:$(NC)"
	@echo "  make publish GITHUB_USERNAME=<username> GITHUB_PAT=<token>"
	@echo "  echo 'GITHUB_USERNAME=myuser' > .env && make publish"
	@echo ""
	@echo "$(YELLOW)Available targets:$(NC)"
	@echo "  $(GREEN)debug$(NC)             - Check if credentials and tools are available"
	@echo "  $(GREEN)versions$(NC)          - Display app and chart versions"
	@echo "  $(GREEN)build$(NC)             - Build the Go binary"
	@echo "  $(GREEN)test$(NC)              - Run Go tests"
	@echo "  $(GREEN)lint$(NC)              - Run go vet"
	@echo "  $(GREEN)tidy$(NC)              - Run go mod tidy"
	@echo "  $(GREEN)docker-build$(NC)      - Build Docker image"
	@echo "  $(GREEN)docker-push$(NC)       - Push Docker image to GHCR"
	@echo "  $(GREEN)helm-package$(NC)      - Package Helm chart as .tgz"
	@echo "  $(GREEN)helm-publish$(NC)      - Publish Helm chart as OCI artifact"
	@echo "  $(GREEN)publish$(NC)           - Build, package, and publish everything"
	@echo "  $(GREEN)clean$(NC)             - Remove local build artifacts"
	@echo ""
	@echo "$(YELLOW)Examples:$(NC)"
	@echo "  make versions"
	@echo "  make build"
	@echo "  make publish GITHUB_USERNAME=myuser GITHUB_PAT=ghp_xxxx"
	@echo ""

versions:
	@echo "$(GREEN)Version Information:$(NC)"
	@echo "  Chart File:    $(CHART_FILE)"
	@echo "  App Version:   $(APP_VERSION)"
	@echo "  Chart Version: $(CHART_VERSION)"
	@echo ""
	@echo "$(GREEN)Docker Image:$(NC)"
	@echo "  $(DOCKER_IMAGE)"
	@echo ""
	@echo "$(GREEN)OCI Artifact:$(NC)"
	@echo "  $(OCI_ARTIFACT)"
	@echo ""

build:
	go build -o $(BINARY) ./cmd/secretweave

test:
	go test ./...

lint:
	go vet ./...

tidy:
	go mod tidy

docker-build: versions
	@echo "$(YELLOW)Building Docker image: $(DOCKER_IMAGE)$(NC)"
	docker build -t $(DOCKER_IMAGE) .
	@echo "$(GREEN)✓ Docker image built successfully$(NC)"

docker-push: docker-build
	@if [ -z "$(GITHUB_USERNAME)" ] || [ -z "$(GITHUB_PAT)" ]; then \
		echo "$(RED)Error: GITHUB_USERNAME and GITHUB_PAT are required$(NC)"; \
		exit 1; \
	fi
	@echo "$(YELLOW)Logging in to GHCR...$(NC)"
	echo "$(GITHUB_PAT)" | docker login $(REGISTRY) -u $(GITHUB_USERNAME) --password-stdin
	@echo "$(YELLOW)Pushing Docker image to GHCR...$(NC)"
	docker push $(DOCKER_IMAGE)
	@echo "$(GREEN)✓ Docker image pushed: $(DOCKER_IMAGE)$(NC)"
	docker logout $(REGISTRY)

helm-package: versions
	@echo "$(YELLOW)Packaging Helm chart...$(NC)"
	cd helm && helm package .
	@echo "$(GREEN)✓ Helm chart packaged: helm/$(HELM_CHART)$(NC)"

helm-publish: helm-package
	@if [ -z "$(GITHUB_USERNAME)" ] || [ -z "$(GITHUB_PAT)" ]; then \
		echo "$(RED)Error: GITHUB_USERNAME and GITHUB_PAT are required$(NC)"; \
		exit 1; \
	fi
	@if [ ! -x "$(ORAS)" ]; then \
		echo "$(RED)Error: oras CLI not found$(NC)"; \
		echo "$(YELLOW)Tried: $(ORAS)$(NC)"; \
		echo "$(YELLOW)Please ensure oras is installed and in PATH$(NC)"; \
		echo "$(YELLOW)Or set: make helm-publish ORAS_PATH=/path/to/oras$(NC)"; \
		exit 1; \
	fi
	@echo "$(YELLOW)Using oras: $(ORAS)$(NC)"
	@echo "$(YELLOW)Logging in to GHCR with oras...$(NC)"
	$(ORAS) login -u $(GITHUB_USERNAME) -p $(GITHUB_PAT) $(REGISTRY)
	@echo "$(YELLOW)Publishing Helm chart as OCI artifact...$(NC)"
	cd helm && $(ORAS) push $(OCI_ARTIFACT) \
		--annotation "org.opencontainers.image.source=https://github.com/blazingbrainz/secretweave" \
		--annotation "org.opencontainers.image.description=SecretWeave: Kubernetes operator that syncs annotated Secrets across namespaces" \
		--annotation "org.opencontainers.image.licenses=MIT" \
		$(HELM_CHART):application/vnd.cncf.helm.chart.v1.tar+gzip
	@echo "$(GREEN)✓ Helm chart published: $(OCI_ARTIFACT)$(NC)"

publish: versions
	@if [ -z "$(GITHUB_USERNAME)" ] || [ -z "$(GITHUB_PAT)" ]; then \
		echo "$(RED)Error: GITHUB_USERNAME and GITHUB_PAT are required$(NC)"; \
		echo ""; \
		echo "$(YELLOW)Usage:$(NC) make publish GITHUB_USERNAME=<user> GITHUB_PAT=<token>"; \
		exit 1; \
	fi
	@echo "$(GREEN)========================================$(NC)"
	@echo "$(GREEN)SecretWeave Build & Publish Pipeline$(NC)"
	@echo "$(GREEN)========================================$(NC)"
	@echo ""
	@$(MAKE) docker-push GITHUB_USERNAME=$(GITHUB_USERNAME) GITHUB_PAT=$(GITHUB_PAT)
	@echo ""
	@$(MAKE) helm-publish GITHUB_USERNAME=$(GITHUB_USERNAME) GITHUB_PAT=$(GITHUB_PAT)
	@echo ""
	@echo "$(GREEN)========================================$(NC)"
	@echo "$(GREEN)✓ Publish Complete!$(NC)"
	@echo "$(GREEN)========================================$(NC)"
	@echo ""
	@echo "$(YELLOW)Artifacts Published:$(NC)"
	@echo "  Docker Image: $(DOCKER_IMAGE)"
	@echo "  OCI Artifact: $(OCI_ARTIFACT)"
	@echo ""
	@echo "$(YELLOW)Pull commands:$(NC)"
	@echo "  docker pull $(DOCKER_IMAGE)"
	@echo "  helm pull oci://$(OCI_ARTIFACT)"
	@echo ""

clean:
	@echo "$(YELLOW)Cleaning build artifacts...$(NC)"
	@rm -f $(BINARY)
	@rm -f helm/$(HELM_CHART)
	@echo "$(GREEN)✓ Cleaned: $(BINARY), helm/$(HELM_CHART)$(NC)"
	@echo "$(YELLOW)Note: Docker image must be removed manually:$(NC)"
	@echo "  docker rmi $(DOCKER_IMAGE)"
