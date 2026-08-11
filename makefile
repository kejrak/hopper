BIN := bin/hopper

build:
	go build -o $(BIN) .

run: build
	./$(BIN)

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not installed — see https://golangci-lint.run/usage/install/"; exit 1; }
	golangci-lint run

.PHONY: build run fmt vet test lint
