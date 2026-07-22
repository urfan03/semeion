# Contributing to semeion

Thanks for your interest. semeion is Apache-2.0 and welcomes contributions.

## Principles

Three rules shape the codebase — please keep to them:

1. **Detection is deterministic and explainable.** The engine, correlation, and
   ranking are classical statistics and rules, not models. An LLM may *explain*
   a finding; it must never be what *produces* one. If a change makes a result
   non-reproducible, it belongs behind the `model.Provider` seam, not in the
   hot path.
2. **Zero required dependencies.** The binary is pure Go standard library. Heavy
   or research-grade models live in the optional Python plane and are reached
   over HTTP with an automatic Go fallback — enabling the plane can only add
   capability, never break a run. Do not add a Go module dependency without a
   discussion first.
3. **Never invent data.** No imputing missing values, no fabricated samples, no
   placeholder metrics. If the input can't support an answer, return an error or
   say so — a wrong number is worse than no number.

## Development

```sh
go build ./...     # single static binary, no codegen
go test ./...      # every package has tests
gofmt -l .         # must print nothing
go vet ./...       # must be clean
```

- Match the surrounding style: comment *why*, not *what*; keep names and idioms
  consistent with the file you are in.
- Every new detector, adapter, sink, or endpoint needs a test. Table-driven
  where it fits; `httptest` for anything over HTTP.
- Public behaviour changes go in `CHANGELOG.md` under `[Unreleased]`.

## Adding things

- **A datafeed** — implement `datafeed.Source` (pull) and/or an OTLP path
  (push). Map external dimensions to `DataPoint.Fields` / `Values`.
- **An alert sink** — implement `alert.Sink`; the notifier handles the score
  floor and dedup.
- **A heavy model** — extend `model.Provider` (and its Python counterpart);
  always keep the pure-Go implementation as the default and fallback.

## Reporting

Open an issue with a minimal reproduction — ideally a small CSV or a job JSON
plus the command you ran. Security-sensitive reports: please disclose privately
first.
