VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS = -s -w -X main.version=$(VERSION)

.PHONY: build test lint bench bench-ci clean docker release install-goreleaser

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/geryon ./cmd/geryon

test:
	go test -race -cover ./...

lint:
	go vet ./...
	@if [ "$(gofmt -s -l . | wc -l)" -gt 0 ]; then \
		echo "gofmt errors in the following files:"; \
		gofmt -s -l .; \
		exit 1; \
	fi
	@which gosec > /dev/null || go install github.com/securego/gosec/v2/cmd/gosec@latest
	gosec ./...

bench:
	go test -bench=. -benchmem -run=^$$ ./benchmarks/...

bench-ci:
	go test -bench=. -benchmem -run=^$$ -count=3 ./benchmarks/... 2>&1 | tee bench_results.txt

clean:
	rm -rf bin/

docker:
	docker build -t ghcr.io/geryonproxy/geryon:$(VERSION) .

install-goreleaser:
	which goreleaser || go install github.com/goreleaser/goreleaser@latest

release: install-goreleaser
	goreleaser release --clean