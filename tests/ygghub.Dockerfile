# syntax=docker/dockerfile:1.7
# Yggdrasil backbone hub. The vault's mesh node only dials OUT (no inbound peer listener), so a
# standalone yggdrasil-go node with a static Listen is the rendezvous all vault nodes peer to.
# Version pinned to the project's ratatoskr/yggdrasil dependency for protocol compatibility.
FROM golang:1.26.3 AS build
ENV CGO_ENABLED=0
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOBIN=/out go install github.com/yggdrasil-network/yggdrasil-go/cmd/yggdrasil@v0.5.14

FROM alpine:3.20
RUN apk add --no-cache jq ca-certificates
COPY --from=build /out/yggdrasil /usr/local/bin/yggdrasil
COPY tests/ygghub-entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
EXPOSE 7777
ENTRYPOINT ["/entrypoint.sh"]
