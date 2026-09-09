BINARY := local-data-api
PKG    := ./cmd/local-data-api

.PHONY: all build run test cover lint fmt vet tidy docker clean

all: fmt vet test build

build:
	CGO_ENABLED=0 go build -trimpath -o bin/$(BINARY) $(PKG)

run:
	go run $(PKG)

test:
	go test -race ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

fmt:
	gofmt -l -w .

vet:
	go vet ./...

lint: fmt vet

tidy:
	go mod tidy

docker:
	docker build -t sonnh96/local-data-api .

clean:
	rm -rf bin coverage.out
