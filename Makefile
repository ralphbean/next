BIN := next-up

.PHONY: all build test vet clean

all: build test

build:
	go build -o $(BIN) .

test: vet
	go test ./...

vet:
	go vet ./...

clean:
	rm -f $(BIN)
