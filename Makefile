ROBOT_CONFIG ?= robot.json
BINARY_NAME  ?= gorai-lights
TARGET       ?= $(shell go env GOOS)/$(shell go env GOARCH)

.DEFAULT_GOAL := help

.PHONY: build compile run validate test tidy clean help

build: ## Compile the robot binary (plain go build -- no gorai CLI needed)
	go build -o bin/$(BINARY_NAME) .

compile: ## Type-check everything
	go build ./...

run: ## Run the robot (requires the gorai CLI)
	gorai run $(ROBOT_CONFIG)

validate: ## Validate robot.json (requires the gorai CLI)
	gorai validate $(ROBOT_CONFIG)

test: ## Run tests
	go test ./...

tidy: ## Resolve module dependencies
	go mod tidy

clean: ## Remove build artifacts
	rm -rf bin/

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
