BINARY_NAME=gateway
BUILD_DIR=bin
CMD_DIR=cmd/gateway
DOCKER_IMAGE=api-gateway
DOCKER_TAG?=latest

.PHONY: all build test lint run docker-build docker-push clean bench coverage

all: lint test build

build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME) ./$(CMD_DIR)/...

test:
	@echo "Running tests..."
	go test -race -count=1 ./...

test-unit:
	@echo "Running unit tests..."
	go test -race -count=1 ./tests/unit/...

test-integration:
	@echo "Running integration tests..."
	go test -race -count=1 -tags=integration ./tests/integration/...

lint:
	@echo "Running linter..."
	golangci-lint run ./...

run:
	@echo "Starting gateway..."
	go run ./$(CMD_DIR)/... -config config/gateway.yaml

docker-build:
	@echo "Building Docker image $(DOCKER_IMAGE):$(DOCKER_TAG)..."
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

docker-push:
	@echo "Pushing Docker image..."
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)

bench:
	@echo "Running benchmarks..."
	go test -bench=. -benchmem -run=^$$ ./...

coverage:
	@echo "Generating coverage report..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out coverage.html

tidy:
	go mod tidy

vet:
	go vet ./...

.DEFAULT_GOAL := build
