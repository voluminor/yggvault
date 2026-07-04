# syntax=docker/dockerfile:1.7
# Verifier / client. Runs `go get`+`go build` (GOPROXY=<node>) and `composer install` (vault-only repo)
# against the running nodes, entirely inside the container — never touches the host Go/PHP environment.
FROM golang:1.26.3
RUN apt-get update && apt-get install -y --no-install-recommends \
        php-cli php-zip php-xml php-mbstring unzip git ca-certificates jq curl openssl xz-utils \
 && rm -rf /var/lib/apt/lists/* \
 && curl -sS https://getcomposer.org/installer | php -- --install-dir=/usr/local/bin --filename=composer
# Zig toolchain (pinned, amd64) — lets the verifier really run `zig fetch` on the universal .tar.gz that
# the generated build.zig.zon `.url` snippet points to, proving the Zig snippet is fetchable end-to-end.
ARG ZIG_VERSION=0.13.0
RUN curl -fsSL "https://ziglang.org/download/${ZIG_VERSION}/zig-linux-x86_64-${ZIG_VERSION}.tar.xz" \
      | tar -xJ -C /opt \
 && ln -s "/opt/zig-linux-x86_64-${ZIG_VERSION}/zig" /usr/local/bin/zig
COPY tests/verifier/run-tests.sh /run-tests.sh
# Committed consumer fixtures (Go module + composer) — the verifier copies these into /work and points
# them at each node. They live in tests/ (committed), NOT generated in the ephemeral tmp/.
COPY tests/stub /stub
# /work writable by the mapped host uid (container runs as ${HOST_UID}, not root).
RUN chmod +x /run-tests.sh && mkdir -p /work && chmod 1777 /work
WORKDIR /work
ENTRYPOINT ["/run-tests.sh"]
