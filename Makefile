.PHONY: build run clean test fmt lint

BINARY_NAME=asdl-agent
BUILD_DIR=bin

build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) cmd/agent/main.go
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

run: build
	./$(BUILD_DIR)/$(BINARY_NAME) -config config.yaml

run-dev:
	go run cmd/agent/main.go -config config.yaml

test:
	go test -v ./...

fmt:
	go fmt ./...

lint:
	go vet ./...

clean:
	@echo "Cleaning..."
	rm -rf $(BUILD_DIR)
	rm -rf /tmp/asdl

deps:
	go mod download
	go mod tidy

install: build
	@echo "Installing to /usr/local/bin/..."
	sudo cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/
	@echo "Installed!"

docker-build:
	docker build -t asdl-agent .

docker-run:
	docker run -d --name asdl-agent \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v /tmp/asdl:/tmp/asdl \
		-e ASDL_HUB_URL=http://10.100.0.1:8080 \
		-e ASDL_VPN_IP=10.100.0.2 \
		asdl-agent

help:
	@echo "Available targets:"
	@echo "  build        - Build the binary"
	@echo "  run          - Build and run with config.yaml"
	@echo "  run-dev      - Run with go run"
	@echo "  test         - Run tests"
	@echo "  fmt          - Format code"
	@echo "  lint         - Lint code"
	@echo "  clean        - Clean build artifacts"
	@echo "  deps         - Download dependencies"
	@echo "  install      - Install to /usr/local/bin"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-run   - Run Docker container"