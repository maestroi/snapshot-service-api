# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get

# Name of the binary to be built
BINARY_NAME=snapshot-service-api

# Directories
CMD_DIR=./cmd
BIN_DIR=./bin
SRC_DIRS := $(shell find . -name '*.go' -not -path "./vendor/*")

# Build flags
BUILD_TAGS=

# Build the project
build:
	$(GOBUILD) -tags "$(BUILD_TAGS)" -o $(BIN_DIR)/$(BINARY_NAME) $(CMD_DIR)/main.go

# Clean the project
clean:
	$(GOCLEAN)
	rm -rf $(BIN_DIR)

# Test the project
test:
	$(GOTEST) -v ./...

# Install project dependencies
deps:
	$(GOGET) -u

redeploy:
	docker-compose down
	docker-compose build --no-cache
	docker-compose up -d

.PHONY: build clean test deps
