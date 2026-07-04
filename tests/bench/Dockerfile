# syntax=docker/dockerfile:1.7
# Storage-engine microbench image. Built twice (CGO 0/1) — CGO_ENABLED flips Pebble's zstd codec
# (pure-Go klauspost vs C DataDog), which is the only axis that depends on the build.
ARG GO_IMAGE=golang:1.26.3

FROM ${GO_IMAGE} AS build
ARG CGO_ENABLED=0
WORKDIR /src
COPY . .
ENV GOWORK=off GOFLAGS=-mod=readonly CGO_ENABLED=${CGO_ENABLED}
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -o /out/storagebench ./tests/bench/storage

# debian-slim has glibc (CGO=1 binary runs); a CGO=0 static binary runs here too.
FROM debian:bookworm-slim
COPY --from=build /out/storagebench /usr/local/bin/storagebench
ENTRYPOINT ["/usr/local/bin/storagebench"]
