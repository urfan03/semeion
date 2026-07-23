.PHONY: build test vet fmt fmtcheck demo tidy race all

all: fmtcheck vet test build

build:
	go build ./...

test:
	go test ./...

race:
	CGO_ENABLED=1 go test -race ./...

bench:
	go test -run '^$$' -bench . -benchmem ./...

bench-nab:
	@test -n "$(NAB)" || (echo "set NAB=/path/to/nab (a checkout of numenta/NAB)"; exit 1)
	go run ./cmd/semeion nab --csv "$(NAB)/data/$(FILE)" --windows "$(NAB)/labels/$(FILE).windows.json"

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
