.PHONY: build test vet fmt fmtcheck demo tidy all

all: fmtcheck vet test build

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# CI-friendly: fail if anything is unformatted.
fmtcheck:
	@test -z "$$(gofmt -l .)" || (echo "unformatted files:" && gofmt -l . && exit 1)

demo:
	go run ./cmd/semeion demo

tidy:
	go mod tidy
