# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
BINARY_NAME=mqtt-exporter

VERSION=1.0.1

COMMIT=$(shell git rev-list -1 HEAD --abbrev-commit)
DATE=$(shell date -u '+%Y-%m-%d')

all: test build

deps: 
build: 
		$(GOBUILD) -ldflags " \
		-X main.version=${VERSION} \
		-X main.buildHash=${COMMIT} \
		-X main.buildDate=${DATE}" \
		-o $(BINARY_NAME) -v
test: 
		$(GOTEST) -v ./...
clean: 
		$(GOCLEAN)
		rm -f $(BINARY_NAME)
