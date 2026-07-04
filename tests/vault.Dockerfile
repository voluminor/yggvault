# syntax=docker/dockerfile:1.7
# Vault node image. Build context = repo root. The project source (incl. generated target/) is
# COPYed into the build stage and compiled there: the host working tree (go.mod/go.sum) is never
# touched. Driver is selected by CGO_ENABLED: 0 => modernc.org/sqlite (static), 1 => mattn (glibc).
ARG GO_IMAGE=golang:1.26.3
ARG RUNTIME=alpine:3.20

FROM ${GO_IMAGE} AS build
ARG CGO_ENABLED=0
# Optional native CPU tuning: GOAMD64=v3 lets the Go compiler emit AVX2/BMI2/FMA (Zen3+/Haswell+);
# CGO_CFLAGS='-O2 -march=znver3' tunes the C SQLite/zstd. A vN binary SIGILLs on an older CPU — build
# it for the exact target microarch. Empty by default (portable baseline).
ARG GOAMD64=
ARG CGO_CFLAGS=
WORKDIR /src
COPY . .
ENV CGO_ENABLED=${CGO_ENABLED}
ENV GOAMD64=${GOAMD64}
ENV CGO_CFLAGS=${CGO_CFLAGS}
# Alpine build image lacks bash (go generate's cleanup directive calls it) and, for CGO, the musl C
# toolchain. Building CGO on musl + static linking yields a binary with no libc-version dependency, so
# it runs on any Linux. Debian golang images already ship bash and gcc, so this is a no-op there.
RUN if command -v apk >/dev/null 2>&1; then \
      apk add --no-cache bash; \
      if [ "$CGO_ENABLED" = "1" ]; then apk add --no-cache gcc musl-dev; fi; \
    fi
# Generated code (target/) and go.sum are NOT tracked, so regenerate them in-image: a clean checkout
# builds without any host-side `go generate`. Generation needs the Go workspace (go.work: the
# _generate/* modules are separate) and the ogen binary on PATH (goopenapigen shells out to it,
# pinned to the go.mod version). See gen.go for the directive set.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -eux; \
    ogen_ver="$(awk '$1=="github.com/ogen-go/ogen"{print $2}' go.mod)"; \
    go install "github.com/ogen-go/ogen/cmd/ogen@${ogen_ver}"; \
    export PATH="$(go env GOPATH)/bin:$PATH"; \
    mkdir -p target tmp; \
    go generate .; \
    GOWORK=off go mod download
# Final compile pins GOWORK=off (main module only) and -mod=readonly (a build can never rewrite
# go.mod/go.sum), now that generation has produced target/ and go.sum above.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    EXTLD=""; if [ "$CGO_ENABLED" = "1" ] && command -v apk >/dev/null 2>&1; then EXTLD='-linkmode external -extldflags -static'; fi; \
    GOWORK=off GOFLAGS=-mod=readonly go build -ldflags="-s -w $EXTLD" -trimpath -o /out/yggvault . \
 && /out/yggvault --info >/dev/null 2>&1 || true

FROM ${RUNTIME} AS runtime
# ca-certificates: git-seed nodes fetch release archives over HTTPS from public providers.
# socat: bridges the loopback-only pprof listener to a publishable port (see vault-entrypoint.sh).
RUN if command -v apk >/dev/null 2>&1; then apk add --no-cache ca-certificates curl socat; \
    else apt-get update && apt-get install -y --no-install-recommends ca-certificates curl socat && rm -rf /var/lib/apt/lists/*; fi
COPY --from=build /out/yggvault /usr/local/bin/yggvault
COPY tests/vault-entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
# Config + storage + logs all live under /data (a bind mount into tmp/ on the host).
ENTRYPOINT ["/entrypoint.sh"]
CMD ["/data/config.yml"]
