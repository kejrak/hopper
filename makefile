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

.PHONY: build run fmt vet test
