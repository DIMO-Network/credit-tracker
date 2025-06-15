.PHONY: clean run build install dep test lint format docker

SHELL := /bin/bash
PATHINSTBIN = $(abspath ./bin)
SCRIPTS_DIR = $(abspath ./scripts)
export PATH := $(PATHINSTBIN):$(SCRIPTS_DIR):$(PATH)

BIN_NAME					?= credit-tracker
DEFAULT_INSTALL_DIR			:= $(go env GOPATH)/$(PATHINSTBIN)
DEFAULT_ARCH				:= $(shell go env GOARCH)
DEFAULT_GOOS				:= $(shell go env GOOS)
ARCH						?= $(DEFAULT_ARCH)
GOOS						?= $(DEFAULT_GOOS)
INSTALL_DIR					?= $(DEFAULT_INSTALL_DIR)
.DEFAULT_GOAL 				:= run


VERSION   := $(shell git describe --tags || echo "v0.0.0")
VER_CUT   := $(shell echo $(VERSION) | cut -c2-)

# Dependency versions
GOLANGCI_VERSION   = latest
PROTOC_VERSION             = 28.3
PROTOC_GEN_GO_VERSION      = $(shell go list -m -f '{{.Version}}' google.golang.org/protobuf)
PROTOC_GEN_GO_GRPC_VERSION = v1.5.1

help:
	@echo "\nSpecify a subcommand:\n"
	@grep -hE '^[0-9a-zA-Z_-]+:.*?## .*$$' ${MAKEFILE_LIST} | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[0;36m%-20s\033[m %s\n", $$1, $$2}'
	@echo ""

build: ## build the binary
	@CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(ARCH) \
		go build -o $(PATHINSTBIN)/$(BIN_NAME) ./cmd/$(BIN_NAME)

run: build ## run the binary
	@./$(PATHINSTBIN)/$(BIN_NAME)

all: clean target

clean: ## clean the binary
	@rm -rf $(PATHINSTBIN)
	
install: build ## install the binary
	@install -d $(INSTALL_DIR)
	@rm -f $(INSTALL_DIR)/$(BIN_NAME)
	@cp $(PATHINSTBIN)/$(BIN_NAME) $(INSTALL_DIR)/$(BIN_NAME)

tidy: ## tidy the go mod
	@go mod tidy

test: ## run tests
	@go test ./...

lint: ## run linter
	@PATH=$$PATH golangci-lint run --timeout 10m

docker: dep ## build docker image
	@docker build -f ./Dockerfile . -t dimozone/$(BIN_NAME):$(VER_CUT)
	@docker tag dimozone/$(BIN_NAME):$(VER_CUT) dimozone/$(BIN_NAME):latest

tools-golangci-lint: ## install golangci-lint
	@mkdir -p $(PATHINSTBIN)
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | BINARY=golangci-lint bash -s -- ${GOLANGCI_VERSION}

tools-protoc: ## install protoc
	@mkdir -p $(PATHINSTBIN)
	rm -rf $(PATHINSTBIN)/protoc
ifeq ($(shell uname | tr A-Z a-z), darwin)
	curl -L https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-osx-x86_64.zip > bin/protoc.zip
endif
ifeq ($(shell uname | tr A-Z a-z), linux)
	curl -L https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-x86_64.zip > bin/protoc.zip
endif
	unzip -o $(PATHINSTBIN)/protoc.zip -d $(PATHINSTBIN)/protoclib 
	mv -f $(PATHINSTBIN)/protoclib/bin/protoc $(PATHINSTBIN)/protoc
	rm -rf $(PATHINSTBIN)/include
	mv $(PATHINSTBIN)/protoclib/include $(PATHINSTBIN)/ 
	rm $(PATHINSTBIN)/protoc.zip

migration: ## Generate migration file specify name with name=your_migration_name
	go tool goose create ${name} sql -s --dir=./pkg/migrate/migrations

make tools: tools-golangci-lint tools-protoc tools-protoc-gen-go tools-protoc-gen-go-grpc tools-sqlboiler-psql ## install all tools

generate: generate-swagger generate-go generate-grpc ## run all file generation for the project

generate-swagger: ## generate swagger documentation
	@go tool swag -version
	go tool swag init -g cmd/credit-tracker/main.go --parseDependency --parseInternal

generate-go:## run go generate
	@go generate ./...

generate-grpc: ## generate grpc files
	@PATH=$$PATH protoc --version
	@PATH=$$PATH protoc --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    pkg/grpc/*.proto

generate-sqlboiler: build
	docker compose up -d postgresql
	sleep 5
	DB_USER=dimo DB_PASSWORD=dimo DB_HOST=localhost DB_PORT=5432 DB_NAME=credit_tracker $(PATHINSTBIN)/$(BIN_NAME) -migrate-only
	@PATH=$$PATH go tool sqlboiler psql --no-tests --wipe
	docker compose down
