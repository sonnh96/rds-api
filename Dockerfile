# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build

WORKDIR /src

# Download dependencies first so they stay cached across source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

# Both drivers are pure Go, so the binary needs no libc at runtime.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
        -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/local-data-api \
        ./cmd/local-data-api

FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="local-data-api" \
      org.opencontainers.image.description="Local AWS Aurora Serverless Data API" \
      org.opencontainers.image.source="https://github.com/sonnh96/local-data-api" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build /out/local-data-api /local-data-api

EXPOSE 80
USER nonroot:nonroot

ENTRYPOINT ["/local-data-api"]
