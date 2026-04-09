VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS = -s -w -X main.version=$(VERSION)

.PHONY: build test clean lint docker release

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/geryon ./cmd/geryon

test:
	go test -race -cover ./...

lint:
	go vet ./...

clean:
	rm -rf bin/

docker:
	docker build -t ghcr.io/geryonproxy/geryon:$(VERSION) .

release:
	mkdir -p bin
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/geryon-linux-amd64 ./cmd/geryon
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/geryon-linux-arm64 ./cmd/geryon
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/geryon-darwin-amd64 ./cmd/geryon
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/geryon-darwin-arm64 ./cmd/geryon
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/geryon-windows-amd64.exe ./cmd/geryon
