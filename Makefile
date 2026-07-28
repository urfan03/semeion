.PHONY: build test vet fmt fmtcheck demo tidy race all bench bench-nab corpus-nab corpus-ucr gate-nab gate-ucr frontier

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

# Score the whole corpus with one detector. DETECTOR defaults to the ensemble;
# run `semeion nab-corpus -h` for the full list.
corpus-nab:
	@test -n "$(NAB)" || (echo "set NAB=/path/to/nab (a checkout of numenta/NAB)"; exit 1)
	go run ./cmd/semeion nab-corpus --dir "$(NAB)" --detector "$(or $(DETECTOR),fisher)"

# Same, against the UCR Anomaly Archive (the benchmark NAB's critics recommend).
corpus-ucr:
	@test -n "$(UCR)" || (echo "set UCR=/path/to/UCR_TimeSeriesAnomalyDatasets"; exit 1)
	go run ./cmd/semeion nab-corpus --ucr "$(UCR)" --detector "$(or $(DETECTOR),fisher)"

gate-ucr:
	@test -n "$(UCR)" || (echo "set UCR=/path/to/UCR_TimeSeriesAnomalyDatasets"; exit 1)
	SEMEION_UCR_DIR="$(UCR)" go test ./benchmark -run TestNABCorpusGate -v -count=1 -timeout 90m

# Walk the precision stack and print the recall/precision Pareto frontier.
frontier:
	@test -n "$(NAB)" || (echo "set NAB=/path/to/nab (a checkout of numenta/NAB)"; exit 1)
	SEMEION_NAB_DIR="$(NAB)" go test ./benchmark -run TestPrecisionStack -v -count=1 -timeout 60m

# Accuracy regression gate: every detector must beat the engine and hold its floor.
gate-nab:
	@test -n "$(NAB)" || (echo "set NAB=/path/to/nab (a checkout of numenta/NAB)"; exit 1)
	SEMEION_NAB_DIR="$(NAB)" go test ./benchmark -run TestNABCorpusGate -v -count=1 -timeout 30m

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
