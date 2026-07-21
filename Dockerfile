# syntax=docker/dockerfile:1

# ── build ────────────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
# CGO off → a fully static binary for a scratch/distroless runtime.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/semeion ./cmd/semeion

# ── runtime ──────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/semeion /semeion
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/semeion"]
CMD ["serve", "--addr=:8080"]
