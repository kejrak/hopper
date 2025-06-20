build: 
	go build -o bin/hopper main.go

run: build
	./bin/hopper