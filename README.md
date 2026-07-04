# yggvault

Canonical repository: [github.com/voluminor/yggvault](https://github.com/voluminor/yggvault).

`yggvault` is a self-contained release archive mirror. It reads versions from upstream repositories or from other
`yggvault` nodes, stores them locally, and republishes ready-to-use artifacts over HTTP/HTTPS and Yggdrasil:
Go module proxy routes, Composer v2 metadata, universal `.zip`/`.tar.gz` archives, JSON APIs, Web UI pages,
Open Graph images, Atom feeds, and generated sitemaps.

The core model is simple: `release_mirrors` maps a local package key to an upstream URL. The node fetches release
versions, normalizes each source tree, builds derived artifacts, and serves them under its own host.

```mermaid
flowchart TB
  upstream["Git upstream or yggvault brother"] --> source["source discovery and fetch"]
  source --> archive["archive validation"]
  archive --> overlay["Go, Composer, universal overlays"]
  overlay --> storage["durable storage"]
  storage --> hot["hot artifact cache"]
  storage --> api["HTTP/Ygg API, Go proxy, Composer, Web UI"]
  hot --> api
  api --> clients["go, composer, browser, curl"]
```

`yggvault` is not a git server and it is not a transparent GOPROXY cache for the whole Go ecosystem. It mirrors
selected release versions and republishes them under the node path: `web.server.domain/key` or
`<PublicKey>.pk.ygg/key`.

One important self-hosting feature: the node can serve your own static frontend from `/` while moving mirror routes
under a prefix such as `/pkg`. That makes one binary both a website and a package mirror backend. See
[Custom frontend and nested mode](#custom-frontend-and-nested-mode).

Common CLI commands such as `--make-preset`, `--make-ygg-key`, `--validate-config`, `--inspect`, `--prune`,
`--vacuum`, and `--rebuild-cache` are listed in [CLI](#cli).

## Contents

- [Quick Choice](#quick-choice)
- [When It Fits](#when-it-fits)
- [Ready-Made Releases](#ready-made-releases)
  - [Available Release Assets](#available-release-assets)
  - [Download a Selected Asset](#download-a-selected-asset)
  - [Windows PowerShell](#windows-powershell)
- [Quick Start](#quick-start)
  - [Common Problems](#common-problems)
- [Ready Config Fragments](#ready-config-fragments)
  - [Production Behind a Reverse Proxy](#production-behind-a-reverse-proxy)
  - [TLS Inside yggvault](#tls-inside-yggvault)
  - [Disable All Metrics](#disable-all-metrics)
  - [Public JSON Metrics and Prometheus/VictoriaMetrics](#public-json-metrics-and-prometheusvictoriametrics)
  - [HTTP, Yggdrasil, and RPC Limits](#http-yggdrasil-and-rpc-limits)
  - [Storage Quota and Archive Limits](#storage-quota-and-archive-limits)
  - [Aggressive Low-RAM Profile](#aggressive-low-ram-profile)
  - [Upstream Credentials](#upstream-credentials)
  - [Enable Yggdrasil](#enable-yggdrasil)
  - [Brother Seed and Follower](#brother-seed-and-follower)
  - [Profiling](#profiling)
- [Storage and Performance](#storage-and-performance)
- [How to Use It](#how-to-use-it)
  - [Go Module Proxy](#go-module-proxy)
  - [Composer](#composer)
  - [Archives and JSON API](#archives-and-json-api)
  - [Web UI](#web-ui)
  - [Custom Frontend and Nested Mode](#custom-frontend-and-nested-mode)
- [How It Works](#how-it-works)
  - [Application Startup](#application-startup)
  - [Adding a Repository](#adding-a-repository)
  - [Rescan Pipeline](#rescan-pipeline)
  - [Serving a Client Request](#serving-a-client-request)
- [Yggdrasil](#yggdrasil)
- [Brother Sync](#brother-sync)
  - [Sync Flow](#sync-flow)
- [Operations](#operations)
  - [CLI](#cli)
  - [History, Deletions, and Degraded Upstreams](#history-deletions-and-degraded-upstreams)
  - [Security and Limits](#security-and-limits)
- [HTTP Routes](#http-routes)
- [For Developers](#for-developers)
  - [Bootstrap](#bootstrap)
  - [Getting Source from a Mirror](#getting-source-from-a-mirror)
  - [Tests and Benchmarks](#tests-and-benchmarks)
  - [Generation](#generation)
  - [Module Map](#module-map)
- [Self Build](#self-build)
  - [CGO Build for a Specific System](#cgo-build-for-a-specific-system)
- [Project Policy](#project-policy)

## Quick Choice

| Task                                        | Use                                                                | Start here                                                          |
|---------------------------------------------|--------------------------------------------------------------------|---------------------------------------------------------------------|
| Download a ready binary                     | GitHub Releases, pure-Go asset for your OS/architecture            | [Ready-Made Releases](#ready-made-releases)                         |
| Build from source without development setup | release or mirror source archive                                   | [Getting Source from a Mirror](#getting-source-from-a-mirror)       |
| Run a local mirror for one project          | `single` web listener and one `release_mirrors` entry              | [Quick Start](#quick-start)                                         |
| Use the node as a Go proxy                  | `GOPROXY=<node>`, module path `host/[prefix/]key`                  | [Go Module Proxy](#go-module-proxy)                                 |
| Use the node as a Composer repository       | `repositories[].type=composer`                                     | [Composer](#composer)                                               |
| Download archives directly                  | `/{key}/latest`, `/{key}/{version}.zip`, `/{key}/{version}.tar.gz` | [Archives and JSON API](#archives-and-json-api)                     |
| Host your own frontend on the node          | `web.static.dir`, `web.routing.prefix`                             | [Custom Frontend and Nested Mode](#custom-frontend-and-nested-mode) |
| Expose the node in Yggdrasil                | `ygg.pem_key`, peers, host `<PublicKey>.pk.ygg`                    | [Yggdrasil](#yggdrasil)                                             |
| Synchronize several nodes                   | Brother RPC or public fallback                                     | [Brother Sync](#brother-sync)                                       |
| Find a CLI command                          | presets, keygen, validation, inspect, prune, vacuum                | [CLI](#cli)                                                         |
| Tune disk, memory, and speed                | `storage.*`, `cache.*`, build type                                 | [Storage and Performance](#storage-and-performance)                 |
| Contribute code                             | generators, hooks, tests, module map                               | [For Developers](#for-developers)                                   |

## When It Fits

- You need a private or public Go/Composer mirror without a separate database or object storage service.
- You want Go, Composer, universal archives, feeds, JSON APIs, and a Web UI from one source of truth.
- You want one binary that owns HTTP/HTTPS ingress, optional Yggdrasil ingress, durable storage, and hot cache.
- You need to survive temporary upstream outages after releases have been mirrored.
- You want several mirrors to synchronize over ordinary web access or over Yggdrasil.
- You want to self-host a static frontend and a package mirror behind the same binary.

Do not choose `yggvault` if you need a transparent proxy for every Go module, git hosting, per-user read ACLs,
a sumdb proxy, or arbitrary commit pseudo-version publishing.

## Ready-Made Releases

Ready binaries are published in [GitHub Releases](https://github.com/voluminor/yggvault/releases). They are produced by
[the release workflow](.github/workflows/release.yml): tests run first, then the workflow creates pure-Go
`CGO_ENABLED=0` binaries for the main platform matrix and separate `amd64` CGO builds.

For the first run, take a pure-Go asset. CGO assets are useful when you intentionally choose `zstd`, `good`, or
`balanced` storage settings and care about C SQLite/C zstd write cost. See [STORAGE-TUNING.md](STORAGE-TUNING.md)
for the measured trade-offs.

The release workflow builds the assets below. Treat this as the build matrix, not a full support matrix. Platforms not
used by the maintainer or CI should be considered best effort until you verify them in your environment.

### Available Release Assets

| Asset                            | Type    | Use it for                          |
|----------------------------------|---------|-------------------------------------|
| `yggvault-linux-amd64`           | pure-Go | Linux x86_64                        |
| `yggvault-linux-arm64`           | pure-Go | Linux arm64/aarch64                 |
| `yggvault-linux-armv7`           | pure-Go | Linux armv7                         |
| `yggvault-linux-386`             | pure-Go | Linux i386/i686                     |
| `yggvault-linux-riscv64`         | pure-Go | Linux riscv64                       |
| `yggvault-windows-amd64.exe`     | pure-Go | Windows x86_64                      |
| `yggvault-windows-arm64.exe`     | pure-Go | Windows arm64                       |
| `yggvault-darwin-amd64`          | pure-Go | macOS Intel                         |
| `yggvault-darwin-arm64`          | pure-Go | macOS Apple Silicon                 |
| `yggvault-freebsd-amd64`         | pure-Go | FreeBSD x86_64                      |
| `yggvault-linux-amd64-cgo`       | CGO     | Linux x86_64 with C SQLite/C zstd   |
| `yggvault-windows-amd64-cgo.exe` | CGO     | Windows x86_64 with C SQLite/C zstd |

If you need another OS, architecture, `GOAMD64`, static musl, or custom `CGO_CFLAGS`, build it yourself:
[Self Build](#self-build).

Current releases publish GitHub-hosted binary assets. Separate checksums, signatures, attestations, and SBOM files are
not published yet. If you require release verification beyond GitHub release provenance, build from a release or mirror
source archive and record your own checksums.

### Download a Selected Asset

```bash
REPO="voluminor/yggvault"
TAG="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' \
  | head -n 1)"
ASSET="yggvault-linux-amd64"

curl -fL "https://github.com/${REPO}/releases/download/${TAG}/${ASSET}" -o yggvault
chmod +x yggvault
./yggvault --info
```

Change `ASSET` to the name you need from the table, for example `yggvault-darwin-arm64` for Apple Silicon,
`yggvault-linux-arm64` for Linux arm64, or `yggvault-freebsd-amd64` for FreeBSD.

If no ready asset fits your target, or if you need custom `GOAMD64`, static CGO, or custom `CGO_CFLAGS`, use
[Self Build](#self-build). For generated source archives that do not need a developer bootstrap, see
[Getting Source from a Mirror](#getting-source-from-a-mirror).

### Windows PowerShell

```powershell
$Repo = "voluminor/yggvault"
$Release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
$Asset = "yggvault-windows-amd64.exe"
$Url = ($Release.assets | Where-Object { $_.name -eq $Asset }).browser_download_url

Invoke-WebRequest $Url -OutFile "yggvault.exe"
.\yggvault.exe --info
```

Use `yggvault-windows-arm64.exe` for Windows arm64.

## Quick Start

After downloading a release binary, create a first config and start the node. If the binary is in the current directory,
use `./yggvault`; if it is installed in `PATH`, use `yggvault`.

```bash
./yggvault --make-preset minimal --out .
```

Minimal working `config.yml`:

```yaml
logging:
  console:
    enabled: true
    level: "info"

web:
  server:
    domain: "modules.localhost"
    mode: "single"
    single:
      proto: "http"
      listen: "127.0.0.1:8080"

ygg:
  pem_key: "./yggvault.pem"
  peers:
    initial:
      - "tls://replace-with-nearby-public-peer.example:443"

storage:
  dir: "./cache"

release_mirrors:
  errors: "https://github.com/go-faster/errors"
```

`release_mirrors` is a map of `local-key -> upstream URL`. The key becomes part of the public path:
`modules.localhost/errors`. Keep keys short and lowercase, and avoid reserved routes such as `latest`, `list`, `@v`,
`packages.json`, `catalog.json`, and `feed.xml`.

Yggdrasil is optional. The preset includes Yggdrasil fields so you can enable mesh access without rewriting the config.

For an HTTP-only first run, disable Yggdrasil:

```yaml
ygg:
  pem_key: ""
  peers:
    initial: [ ]
```

If you want Yggdrasil, create a key before the first run:

```bash
./yggvault --make-ygg-key
```

Then replace the example in `ygg.peers.initial` with 2-3 nearby public peer URIs. Peer lists:
[yggdrasil-network/public-peers](https://github.com/yggdrasil-network/public-peers) and
[publicpeers.neilalexander.dev](https://publicpeers.neilalexander.dev/).

Run:

```bash
./yggvault --validate-config config.yml
./yggvault config.yml
```

The first rescan downloads versions from upstream. Then check the node:

```bash
curl -fsS http://127.0.0.1:8080/health
curl -fsS http://127.0.0.1:8080/catalog.json
curl -fsS http://127.0.0.1:8080/errors/latest

GOPROXY=http://127.0.0.1:8080 GOSUMDB=off \
  go list -m modules.localhost/errors@latest
```

`modules.localhost` does not need to resolve in DNS for the Go command above: the request goes to `GOPROXY`, while the
module path remains the package identity. If you change `web.server.domain`, use the same host in the Go module path.

### Common Problems

| Symptom                                  | Cause                                                                   | Fix                                             |
|------------------------------------------|-------------------------------------------------------------------------|-------------------------------------------------|
| `go.mod requires go >= 1.26.3`           | old Go toolchain                                                        | update Go to the version from `go.mod` or newer |
| server starts, but the key never appears | upstream is unreachable or `release_mirrors` still contains the example | check the URL and wait for rescan               |
| `go get` cannot find the module          | module path does not match `web.server.domain/[prefix/]key`             | use the configured domain and prefix            |
| Go tries to contact `sum.golang.org`     | the republished module path is not in the public sumdb                  | set `GOSUMDB=off` or scoped `GONOSUMDB`         |
| Yggdrasil host does not open             | no key, no peers, or no mesh route                                      | see [Yggdrasil](#yggdrasil)                     |

## Ready Config Fragments

The fragments below are meant to be pasted into `config.yml` on top of a preset. They are not complete configs; they
show the knobs operators usually choose.

Only short recipes live here. Model details, limits, and trade-offs are in [Yggdrasil](#yggdrasil),
[Brother Sync](#brother-sync), [Storage and Performance](#storage-and-performance), and
[Security and Limits](#security-and-limits).

YAML values are read literally. If you need secrets from environment variables, generate the config with an external
template or secret manager; `${TOKEN}` is not expanded by `yggvault`.

### Production Behind a Reverse Proxy

Use `shared` when TLS is terminated by nginx, Caddy, an ingress controller, or another proxy. `yggvault` listens on
plain HTTP on loopback or a container interface, but generates external links as HTTPS.

```yaml
info:
  name: "modules.example.org"
  description: "Go and Composer release mirror"

logging:
  console:
    enabled: true
    level: "info"
  file:
    enabled: true
    level: "info"
    dir: "/var/log/yggvault"
    max_size: "100mb"
    max_backups: 30
    max_age: "336h"
    compress: true

web:
  server:
    domain: "modules.example.org"
    mode: "shared"
    shared:
      listen: "127.0.0.1:8080"

storage:
  dir: "/var/lib/yggvault"
```

### TLS Inside yggvault

Use this only when the process itself owns TLS. If TLS is already terminated by a reverse proxy, use `shared`.

```yaml
web:
  server:
    domain: "modules.example.org"
    mode: "single"
    tls:
      cert: "/etc/letsencrypt/live/modules.example.org/fullchain.pem"
      key: "/etc/letsencrypt/live/modules.example.org/privkey.pem"
    single:
      proto: "https"
      listen: "0.0.0.0:443"
```

### Disable All Metrics

```yaml
metrics:
  web:
    public: false
    internal: false
  ygg:
    public: false
    internal: false
  push:
    enabled: false
```

When public, internal, and push metrics are disabled, telemetry does not collect snapshots and `/metrics/*` is not
published.

### Public JSON Metrics and Prometheus/VictoriaMetrics

```yaml
metrics:
  web:
    public: true
    internal: true
  ygg:
    public: false
    internal: false
  snapshot_interval: "1s"
  push:
    enabled: true
    url: "http://127.0.0.1:8428"
    interval: "15s"
    timeout: "10s"
```

`/metrics` and `/metrics/{core,cache,errors,rescan}` are human-friendly JSON endpoints. `/metrics/internal` is full
Prometheus/OpenMetrics text and should normally be exposed only in a trusted network.

### HTTP, Yggdrasil, and RPC Limits

```yaml
web:
  ingress:
    max_request_uri_bytes: 4096
    read_buffer_size: "32KiB"
    read_header_timeout: "10s"
    idle_timeout: "120s"

rate_limit:
  web:
    http:
      requests_per_second: 20
      burst: 60
      per_peer:
        requests_per_second: 2
        burst: 10
        max_tracked: 4096
    https:
      requests_per_second: 20
      burst: 60
      per_peer:
        requests_per_second: 2
        burst: 10
        max_tracked: 4096
  ygg:
    requests_per_second: 10
    burst: 30
    per_peer:
      requests_per_second: 2
      burst: 10
      max_tracked: 4096

brother:
  rpc:
    max_parallel: 4
    max_parallel_per_peer: 2
    rate_per_sec: 16
    max_fetch_response_bytes: "256MiB"
    max_fetch_batch_count: 256
```

`requests_per_second: 0` disables a specific bucket. Public web nodes should usually keep at least per-peer limits.
See [Security and Limits](#security-and-limits) for the threat model.

### Storage Quota and Archive Limits

```yaml
storage:
  in_flight_read_bytes: "256mb"
  overlay_build_max_parallel: 2
  quota:
    max_total_size: "20gb"
    evict_to_size: "16gb"
    retain_latest_per_key: 5
    max_versions_per_key: 0
    gc_interval: "24h"
  hot:
    max_size: "4gb"
    idle_ttl: "2h"
    retain: "latest"
    verify_on_read: "sampled"
  archive_limits:
    size:
      compressed: "100mb"
      unpacked: "512mb"
      per_file: "64mb"
    entries:
      count: 20000
      path_bytes: 1024

cache:
  metadata_max_size: "64mb"
```

Durable quota limits the source of truth. `storage.hot.max_size` limits regenerable `.zip`/`.tar.gz` and Go/Composer
artifacts separately. Choose numbers from workload profiles: [Storage and Performance](#storage-and-performance) and
[STORAGE-TUNING.md](STORAGE-TUNING.md).

### Aggressive Low-RAM Profile

This is the aggressive README variant. For a less strict profile that keeps more artifacts hot, see
[STORAGE-TUNING.md](STORAGE-TUNING.md).

```yaml
source:
  download_max_parallel: 2

rescan:
  max_parallel_keys: 1

storage:
  in_flight_read_bytes: "64mb"
  overlay_build_max_parallel: 1
  pebble:
    block_cache_size: "32mb"
    memtable_size: "16mb"
  hot:
    max_size: "512mb"
    retain: "none"

cache:
  metadata_max_size: "16mb"
```

This profile lowers baseline memory and parallelism, but it rebuilds artifacts more often and makes rescans slower.

### Upstream Credentials

```yaml
source:
  credentials:
    github: "ghp_replace_with_real_token"
    gitlab: ""
    bitbucket: ""
    others:
      git.example.org: "Authorization: token replace_with_real_token"
```

Credentials apply only to matching provider hosts. They are not sent to Yggdrasil, Brother RPC, or unrelated domains.

### Enable Yggdrasil

Create a key:

```bash
./yggvault --make-ygg-key
```

Add it to the config:

```yaml
ygg:
  pem_key: "./yggvault.pem"
  peers:
    initial:
      - "tls://example-peer-1:443"
      - "tcp://example-peer-2:62486"
    max_per_proto: 1
    probe_timeout: "10s"
    refresh_interval: "1h"
    batch_size: 8
```

When `ygg.pem_key` is empty, the Yggdrasil ingress is disabled. When it is set, the node serves HTTP on the fixed
Yggdrasil service port 80 and gets a host like `<PublicKey>.pk.ygg`. Peer sources and client commands are in
[Yggdrasil](#yggdrasil).

### Brother Seed and Follower

A seed usually points at the real upstream:

```yaml
release_mirrors:
  errors: "https://github.com/go-faster/errors"

brother:
  rpc:
    web_enabled: true
    ygg_enabled: true
    max_parallel: 4
    max_parallel_per_peer: 2
```

A follower can read the same key from the seed. Use the real public key URL, including the prefix when the seed runs
in nested mode.

```yaml
release_mirrors:
  errors: "https://seed.example.org/errors"

brother:
  prefer: "brother"
  transport_order: "web_ygg"
  first_source_timeout: "5s"
  rpc:
    web_enabled: false
    ygg_enabled: true
```

The sync flow, fallback without RPC, and limits are described in [Brother Sync](#brother-sync).

### Profiling

```yaml
profiling:
  enabled: true
  listen: "127.0.0.1:6060"
  max_profile_seconds: 60
```

The profiling listener must be loopback. Use an SSH tunnel for remote diagnostics.

## Storage and Performance

`storage.dir` contains both the durable source of truth and the regenerable hot cache:

| Layer        | Stores                                              | Operator concern                                           |
|--------------|-----------------------------------------------------|------------------------------------------------------------|
| SQLite index | versions, statuses, history events, digest metadata | a commit makes a version visible                           |
| Pebble       | blobs and trees by BLAKE3-24                        | compression, memtable, and block cache affect disk/CPU/RAM |
| hot cache    | ready artifacts for serving                         | can be deleted and rebuilt from durable storage            |
| RAM cache    | small metadata responses                            | limited by `cache.metadata_max_size`                       |

Short choice:

| Scenario                       | Baseline choice                                                    |
|--------------------------------|--------------------------------------------------------------------|
| Ordinary node                  | `compression=minlz`, `CGO_ENABLED=0`, `hot.verify_on_read=sampled` |
| Maximum portable compatibility | `snappy`, `CGO_ENABLED=0`                                          |
| Minimum durable disk           | `zstd`, `CGO_ENABLED=1`                                            |
| Frequent ingest                | increase `storage.pebble.memtable_size`                            |
| Read-heavy large artifacts     | increase `storage.hot.max_size`, review `hot.verify_on_read`       |
| Integrity-critical serving     | `storage.hot.verify_on_read=always`                                |

Detailed profiles, benchmark numbers, native/CGO build effects, and ready config fragments:
[STORAGE-TUNING.md](STORAGE-TUNING.md).

## How to Use It

### Go Module Proxy

For ordinary HTTP/HTTPS ingress:

```bash
GOPROXY=https://modules.example.org GOSUMDB=off \
  go get modules.example.org/errors@v0.7.1
```

If the node should be only the first proxy in a chain:

```bash
GOPROXY=https://modules.example.org,direct GONOSUMDB=modules.example.org \
  go list -m modules.example.org/errors@latest
```

For a Yggdrasil host:

```bash
YGG_HOST="0123456789abcdef.pk.ygg"
GOPROXY="http://${YGG_HOST}" GONOSUMDB="*.pk.ygg" \
  go get "${YGG_HOST}/errors@v0.7.1"
```

Do not set `GOPRIVATE="*.pk.ygg"` for this scenario without explicit `GONOPROXY=none`: by default, `GOPRIVATE` is
inherited by `GONOPROXY` and can make Go bypass the configured `GOPROXY`.

`yggvault` implements GOPROXY routes `@v/list`, `@latest`, `@v/{version}.info`, `@v/{version}.mod`,
`@v/{version}.zip`, including major paths such as `/{key}/vN/...`. It is a mirror-and-republish system, not a
transparent cache:

- only keys from `release_mirrors` are served;
- the module path becomes `web.server.domain/[prefix/]key` or `<PublicKey>.pk.ygg/[prefix/]key`;
- `GOSUMDB=off` or scoped `GONOSUMDB` is usually required;
- canonical release versions are published, not arbitrary commit pseudo-versions;
- if a tree is invalid for Go zip, the version may still be available as a universal archive or Composer package.

`overlay.go.rewrite_enabled` is enabled by default. If the upstream `module` path in `go.mod` differs from the target
path, the overlay rewrites `go.mod` and Go imports for the published module zip.

### Composer

Add the node as a Composer v2 repository:

```json
{
  "repositories": [
    {
      "type": "composer",
      "url": "https://modules.example.org"
    }
  ],
  "require": {
    "monolog/monolog": "*"
  }
}
```

`/packages.json` is the Composer repository root, `/packages/list.json` lists packages, and
`/p2/{vendor}/{package}.json` plus `/p2/{vendor}/{package}~dev.json` serve Composer p2 metadata. Dist archives are
built from the same version tree as the universal zip.

### Archives and JSON API

| Route                     | Format | Purpose                       |
|---------------------------|--------|-------------------------------|
| `/catalog.json`           | JSON   | all keys on the node          |
| `/{key}/latest`           | text   | latest version for the key    |
| `/{key}/list`             | text   | versions newest-first         |
| `/{key}/list/full`        | JSON   | versions with detailed fields |
| `/{key}/releases.json`    | JSON   | paginated release list        |
| `/{key}/{version}.json`   | JSON   | release detail                |
| `/{key}/{version}.zip`    | binary | universal zip                 |
| `/{key}/{version}.tar.gz` | binary | universal tar.gz              |
| `/feed.xml`               | Atom   | global release feed           |
| `/{key}/releases.xml`     | Atom   | per-key release feed          |

Large `.zip` and `.tar.gz` responses support `ETag`, `If-None-Match`, `Last-Modified`, `Accept-Ranges`, `Range`/`206`,
and `HEAD`.

When `web.static.dir` is enabled, user static files own `/`, while package/API/browser routes move under
`web.routing.prefix`, for example `/pkg/{key}/latest`. Stable service routes (`/health`, `/info`, `/metrics`,
`/openapi.json`) stay at the root. See [Custom Frontend and Nested Mode](#custom-frontend-and-nested-mode).

### Web UI

The built-in Web UI shows the catalog, key pages, version pages, install snippets, artifact links, sitemap, and Open
Graph images. If you want your own frontend on the same node, use
[nested mode](#custom-frontend-and-nested-mode).

### Custom Frontend and Nested Mode

`yggvault` can be more than a storage/API backend. It can also serve one self-hosted static site: your frontend comes
from `web.static.dir`, and the package mirror stays in the same process. This works well for documentation, an internal
portal, a public package catalog, or a landing page where install snippets and artifacts should live on the same host.

Without `web.static.dir`, the built-in catalog and API live at the root: `/`, `/{key}`, `/{key}/latest`,
`/{key}/{version}.zip`. When `web.static.dir` is set, nested routing is enabled:

| URL                                             | Owner                | Frontend meaning                              |
|-------------------------------------------------|----------------------|-----------------------------------------------|
| `/`                                             | your static frontend | `web.static.index_file`, usually `index.html` |
| `/assets/app.css`                               | your static frontend | ordinary static assets from `web.static.dir`  |
| `/pkg/`                                         | `yggvault`           | built-in mirror catalog under the prefix      |
| `/pkg/catalog.json`                             | `yggvault`           | JSON catalog for your frontend                |
| `/pkg/{key}`                                    | `yggvault`           | HTML/API key page                             |
| `/pkg/{key}/latest`                             | `yggvault`           | latest version text endpoint                  |
| `/pkg/{key}/{version}.zip`                      | `yggvault`           | universal artifact                            |
| `/pkg/{key}/@v/list`                            | `yggvault`           | Go proxy endpoint                             |
| `/health`, `/info`, `/metrics`, `/openapi.json` | `yggvault`           | service routes stay at the root               |

`/info` stays a root service route and publishes `route_prefix`: an empty string in root mode, or a segment such as
`pkg` when package routes are mounted under a prefix. Brother nodes read `route_prefix` to derive the remote key from
URLs like `https://host/pkg/key`.

Minimal config:

```yaml
web:
  static:
    dir: "./site"
    index_file: "index.html"
    max_size: "256mb"
    cache_max_age: "24h"
    deny:
      - ".env"
      - ".key"
      - ".pem"
      - "/.git/"
      - "/.hg/"
      - "/.svn/"
      - "/config.json"
      - "/config.yaml"
      - "/config.yml"
  routing:
    prefix: "pkg"
  pages:
    sitemap_size: 1000
```

`web.static.dir` must exist, must be readable, and must contain `index_file`. It must not overlap `storage.dir` or
`logging.file.dir`. `web.routing.prefix` is one path segment matching `^[a-z0-9][a-z0-9._-]{0,31}$`; it cannot be a
reserved name such as `health`, `info`, `metrics`, `openapi.json`, `logo`, `og.png`, `favicon.ico`, or `sitemap.xml`.

Keep the API base as a frontend setting, for example `"/pkg"`, instead of hard-coding root paths. Then the same
frontend can be built for nested and non-nested deployments. Install snippets must include the prefix:

```bash
# In nested mode the prefix becomes part of the Go module path.
GOPROXY=https://modules.example.org GOSUMDB=off \
  go get modules.example.org/pkg/errors@latest
```

```json
{
  "repositories": [
    {
      "type": "composer",
      "url": "https://modules.example.org/pkg"
    }
  ]
}
```

Sitemaps are split in nested mode. Root `/sitemap.xml` can be a file from your static frontend. The generated mirror
sitemap is available under the prefix, for example `/pkg/sitemap.xml`; URLs inside it include the host and prefix
(`/pkg/`, `/pkg/{key}`, `/pkg/{key}/{version}`). `web.pages.sitemap_size` caps the generated list. If the cap is hit,
catalog, public metrics when enabled, keys, and newest versions win. For SEO, a common setup is a root sitemap index
from your static frontend that links to `/pkg/sitemap.xml`.

## How It Works

### Application Startup

```mermaid
flowchart TB
  args["CLI args"] --> mode{"one-shot command?"}
  mode -- " make preset / key / validate " --> oneshot["run command and exit"]
  mode -- " config path " --> load["load YAML, JSON or HJSON config"]
  load --> validate["schema + business validation"]
  validate --> runtime["assemble runtime"]
  runtime --> storage["open storage"]
  runtime --> cache["init RAM and hot cache"]
  runtime --> web["start web listeners"]
  runtime --> ygg{"ygg.pem_key set?"}
  ygg -- " yes " --> mesh["start embedded Yggdrasil node and HTTP port 80"]
  ygg -- " no " --> no_mesh["Yggdrasil disabled"]
  runtime --> rescan["start rescan supervisor"]
  runtime --> telemetry{"metrics enabled?"}
  telemetry -- " yes " --> metrics["collect snapshots and expose routes"]
  telemetry -- " no " --> no_metrics["telemetry disabled"]
```

CLI, preset generation, and maintenance entrypoints live in [mod/cli](mod/cli/README.md). Config loading and validation
live in [mod/config](mod/config/README.md).

### Adding a Repository

```mermaid
flowchart TB
  edit["operator edits config.yml: release_mirrors key and URL"]
  validate["yggvault --validate-config config.yml"]
  restart["restart yggvault with config.yml"]
  classify["classify source through /health, /info or Brother.Hello"]
  fetch["list releases and fetch archives"]
  publish["publish normalized versions to storage"]
  visible["/{key}, Go, Composer and archives are visible"]

  edit --> validate --> restart --> classify --> fetch --> publish --> visible
```

Source discovery and egress safety live in [mod/source](mod/source/README.md). Periodic scanning and publish
coordination live in [mod/rescan](mod/rescan/README.md).

### Rescan Pipeline

```mermaid
flowchart TB
  tick["rescan interval"] --> keys["scan release_mirrors keys"]
  keys --> classify["git source or yggvault brother"]
  classify --> fetch["fetch release archive or brother blobs"]
  fetch --> archive["validate archive limits and paths"]
  archive --> overlay["detect Go/Composer and build artifact plan"]
  overlay --> publish["PublishStaged"]
  publish --> durable["Pebble blobs + SQLite metadata"]
  durable --> state["runtime snapshot updated"]
```

Archive safety is in [mod/archive](mod/archive/README.md). Artifact detection and Go/Composer materialization are in
[mod/overlay](mod/overlay/README.md). Storage commit semantics are in [mod/storage](mod/storage/README.md).

### Serving a Client Request

```mermaid
flowchart TB
  req["HTTP or Yggdrasil request"] --> front["method, URI, listener context, rate limit"]
  front --> route["generated route"]
  route --> kind{"small metadata?"}
  kind -- " yes " --> ram["RAM byte cache or build bytes"]
  kind -- " no, artifact " --> hot{"hot file exists?"}
  hot -- " yes " --> file["open and stream file"]
  hot -- " no " --> build["rebuild from durable storage"]
  build --> file
  ram --> resp["response"]
  file --> resp
```

Request framing and route handlers live in [mod/server](mod/server/README.md). Shared service route names live in
[mod/route](mod/route/README.md). HTML rendering lives in [mod/view](mod/view/README.md). RAM byte-cache logic lives
in [mod/cache](mod/cache/README.md).

## Yggdrasil

`yggvault` can expose the same HTTP routes through Yggdrasil. This is useful when a mirror must be reachable inside
the mesh without public IPv4/IPv6 ingress, or when brother nodes should synchronize through the mesh.

You do not need a separate Yggdrasil daemon. When `ygg.pem_key` points to a key file, `yggvault` embeds a Yggdrasil
node and serves HTTP on the fixed Yggdrasil service port 80. Ordinary HTTP/HTTPS mode works without Yggdrasil.

The minimal config fragment is in [Enable Yggdrasil](#enable-yggdrasil). For the first run:

- create a key with `./yggvault --make-ygg-key`;
- add 2-3 nearby peers to `ygg.peers.initial`.

Choose public peers from [yggdrasil-network/public-peers](https://github.com/yggdrasil-network/public-peers) or by
online status at [publicpeers.neilalexander.dev](https://publicpeers.neilalexander.dev/). Do not add far-away peers
just to increase the count; latency and stability of nearby entries matter more.

When `ygg.pem_key` is set, the published host is derived from the public key and looks like `<PublicKey>.pk.ygg`.

Mesh check:

```bash
YGG_HOST="0123456789abcdef.pk.ygg"
curl -fsS "http://${YGG_HOST}/catalog.json"
GOPROXY="http://${YGG_HOST}" GONOSUMDB="*.pk.ygg" \
  go list -m "${YGG_HOST}/errors@latest"
```

Yggdrasil identity, dial/listen integration, and NodeInfo publishing are described in
[mod/mesh](mod/mesh/README.md).

## Brother Sync

Brother sync lets one `yggvault` read another `yggvault` as an upstream. Ready seed/follower YAML examples are in
[Brother Seed and Follower](#brother-seed-and-follower); this section describes the exchange model.

A source is recognized as a brother through `/health`, `/info`, and `Brother.Hello`. If RPC is available, the follower
pulls index pages, trees, and blob batches through `CONNECT /rpc`. If RPC is closed or unreachable, the source layer
can fall back to public `releases.json`, release detail, and universal archives. Fallback is slower than RPC, but it
reads the same read-only data ordinary clients can see.

In nested mode, a follower can point at a brother URL with a prefix, for example `https://seed.example.org/pkg/errors`.
The new node reads `route_prefix` from the seed's `/info` and strips the remote prefix when deriving the key `errors`.
If the remote node is old and does not return `route_prefix`, the previous fallback is used: the local
`web.routing.prefix`.

### Sync Flow

```mermaid
flowchart TB
  start["follower opens configured brother source"]
  hello["CONNECT /rpc and Brother.Hello"]
  rpc{"RPC available?"}
  limits["receive protocol and fetch limits"]
  batches["pull index pages, trees and blob batches"]
  verify_rpc["verify blob hashes and publish"]
  public["read public releases.json and release detail"]
  archive["download universal zip or tar.gz"]
  verify_public["verify tree hash and publish"]

  start --> hello --> rpc
  rpc -- " yes " --> limits --> batches --> verify_rpc
  rpc -- " no " --> public --> archive --> verify_public
```

Main knobs:

| Config                                 | Meaning                                               |
|----------------------------------------|-------------------------------------------------------|
| `brother.prefer`                       | try original source first or brother first            |
| `brother.transport_order`              | web first or Yggdrasil first when both are advertised |
| `brother.rpc.web_enabled`              | expose `CONNECT /rpc` on web listeners                |
| `brother.rpc.ygg_enabled`              | expose `CONNECT /rpc` on the Yggdrasil listener       |
| `brother.rpc.max_parallel`             | total concurrent RPC sessions                         |
| `brother.rpc.max_fetch_response_bytes` | max bytes in one `BlobsFetch` response                |
| `brother.rpc.max_fetch_batch_count`    | max blob hashes in one batch                          |

Treat public RPC as part of the mirror surface. It is read-only, but it still spends IO and bandwidth.

Brother wire DTOs are in [mod/brotherwire](mod/brotherwire/README.md). The server side is in
[mod/server](mod/server/README.md); the client and public fallback path are in [mod/source](mod/source/README.md).

## Operations

### CLI

| Command                                                                              | Purpose                                                                       |
|--------------------------------------------------------------------------------------|-------------------------------------------------------------------------------|
| `yggvault <config.yml>`                                                              | start the server                                                              |
| `yggvault --config <config.yml>`                                                     | same, explicit form                                                           |
| `--validate-config <config.yml>`                                                     | validate config and exit                                                      |
| `--make-preset <minimal\|medium\|full> [--out <dir>] [--format <yaml\|json\|hjson>]` | generate a config preset                                                      |
| `--make-ygg-key [--force]`                                                           | create `./yggvault.pem` and print `<hex>.pk.ygg`                              |
| `--inspect <config.yml>`                                                             | show keys, versions, sizes, and storage orphan estimate                       |
| `--prune <config.yml> [--force]`                                                     | delete storage keys missing from `release_mirrors`; dry-run without `--force` |
| `--vacuum <config.yml>`                                                              | run SQLite VACUUM and Pebble compaction                                       |
| `--rebuild-cache <config.yml>`                                                       | rebuild hot artifacts and digest helpers                                      |
| `--info` or `-i`                                                                     | show project and dependency versions                                          |
| `--help` or `-h`                                                                     | show help                                                                     |

Maintenance commands require exclusive access to storage. Stop the running server before `inspect`, `prune`, `vacuum`,
and `rebuild-cache`.

### History, Deletions, and Degraded Upstreams

`history_policy` controls what happens when upstream changes or removes a version:

| Parameter                             | Behavior                                                        |
|---------------------------------------|-----------------------------------------------------------------|
| `history_policy.mutation=history`     | keep historical pseudo-versions when upstream mutates a version |
| `history_policy.mutation=overwrite`   | overwrite the published version                                 |
| `history_policy.deletion.mode=keep`   | keep versions that disappeared upstream                         |
| `history_policy.deletion.mode=delete` | delete after `grace_cycles` consecutive missing cycles          |
| `history_policy.max_events`           | event limit for Atom feeds and release history                  |

`upstream_availability.permanent_after_cycles` sets how many consecutive failed cycles make a source permanently
unavailable. An error for one key does not cancel the cycle for other keys.

### Security and Limits

- `web.ingress.*` limits request URI size, header read timeout, and idle sockets.
- `rate_limit.web.*` and `rate_limit.ygg.*` provide global and per-peer token buckets.
- `mod/source` blocks SSRF into private addresses after DNS resolution, limits redirects, and uses bounded reads.
- Provider credentials apply only to matching provider hosts.
- `brother.rpc.*` limits public read-only RPC.
- Public brother fallback reads only public metadata/artifact routes and verifies the tree hash before publishing.
- `mod/archive` checks traversal, archive type, entry count, compressed/unpacked/per-file sizes.
- `storage.in_flight_read_bytes`, `storage.archive_limits`, `storage.quota`, and `storage.hot.*` define the main
  resource budgets.
- `profiling.listen` must be loopback.
- Public `info` and Yggdrasil NodeInfo must not contain secrets.

## HTTP Routes

Stable service routes are described in [mod/route](mod/route/README.md). Package-manager routes live near the code
that builds them.

The table below shows root-mode paths. In nested mode, user static files own `/`, while package/browser routes and
generated media move under `web.routing.prefix`, for example `/pkg/catalog.json` and `/pkg/sitemap.xml`; `/health`,
`/info`, `/metrics`, and `/openapi.json` stay at the root.

| Group         | Routes                                                                                                               |
|---------------|----------------------------------------------------------------------------------------------------------------------|
| Service       | `/health`, `/info`, `/openapi.json`                                                                                  |
| Metrics       | `/metrics`, `/metrics/core`, `/metrics/cache`, `/metrics/errors`, `/metrics/rescan`, `/metrics/internal`             |
| Browser/media | `/`, `/{key}`, `/{key}/{version}`, `/favicon.ico`, `/logo/{size}.png`, `/og.png`, `/og/{key}.png`, `/sitemap.xml`    |
| Release API   | `/catalog.json`, `/{key}/releases.json`, `/{key}/{version}.json`, `/{key}/latest`, `/{key}/list`, `/{key}/list/full` |
| Archives      | `/{key}/{version}.zip`, `/{key}/{version}.tar.gz`                                                                    |
| Go proxy      | `/{key}/@latest`, `/{key}/@v/list`, `/{key}/@v/{version}.info`, `/{key}/@v/{version}.mod`, `/{key}/@v/{version}.zip` |
| Composer      | `/packages.json`, `/packages/list.json`, `/p2/{vendor}/{package}.json`, `/p2/{vendor}/{package}~dev.json`            |
| Atom          | `/feed.xml`, `/{key}/releases.xml`                                                                                   |
| Brother       | `CONNECT /rpc`                                                                                                       |

Go routes also support major paths such as `/{key}/vN/@v/list`, `/{key}/vN/@latest`, and related
`@v/{version}` endpoints.

## For Developers

### Bootstrap

For ordinary development from a git clone:

```bash
bash _run/firststart.sh
```

The script creates `target/` and `tmp/`, installs generators, adds git hooks, runs `go work sync`, runs
`go generate .`, runs `go mod tidy` in main modules, and runs `go generate .` again.

Manual setup without hooks:

```bash
mkdir -p target tmp

go install github.com/amazing-generators/goconfgen/cmd/goconfgen@latest
go install github.com/amazing-generators/godepsgen/cmd/godepsgen@latest
go install github.com/amazing-generators/gometagen/cmd/gometagen@latest
go install github.com/amazing-generators/goopenapigen/cmd/goopenapigen@latest

go work sync
go generate .

go list -m -f '{{if .Main}}{{.Dir}}{{end}}' all | while read -r dir; do
  [ -n "$dir" ] || continue
  go -C "$dir" mod tidy
done

go generate .
```

`go generate .` cleans `target/*` and `tmp/*` before generation. Do not store hand-written files there.

### Getting Source from a Mirror

There are two different source workflows:

| Source                                   | Use it when                                                | Important detail                                                      |
|------------------------------------------|------------------------------------------------------------|-----------------------------------------------------------------------|
| Release archive or mirror source archive | you only need to build a binary or inspect release source  | generated files are already included; you can run `go build` directly |
| Git repository                           | you need to edit code, run generators, and inspect history | run [Bootstrap](#bootstrap); this is the development workflow         |

For self builds without development setup, use a release or mirror source archive. This is the recommended build path:
the archive contains generated `target/*` and other generated files, so you do not need to install generators or run
`go generate`.

```bash
BASE="https://www.ratatoskr.space/pkg/yggvault"
# Yggdrasil mirror, if you are already in the mesh:
# BASE="http://[203:b338:2a84:a18f:986:47ae:1a4:d8d3]/pkg/yggvault"

VER="$(curl -fsSL "$BASE/latest")"

mkdir -p tmp/src
curl -fsSL "$BASE/$VER.tar.gz" -o "tmp/yggvault-$VER.tar.gz"
tar -xzf "tmp/yggvault-$VER.tar.gz" -C tmp/src --strip-components=1
cd tmp/src

go build -ldflags="-s -w" -trimpath -o ../yggvault .
```

If you need a git checkout, use it as a developer workspace:

```bash
git clone https://github.com/voluminor/yggvault.git
cd yggvault
bash _run/firststart.sh
```

After that you can edit sources, run `go generate .`, tests, and local builds. A raw git checkout may be incomplete for
direct building until bootstrap has generated the local files.

### Tests and Benchmarks

```bash
go test ./...
bash tests/scripts/up.sh --verify
REPS=3 bash tests/bench/run.sh
CONC=200 DUR=20 bash tests/bench/highload.sh
```

| Command                        | What it checks                                                             |
|--------------------------------|----------------------------------------------------------------------------|
| `go test ./...`                | unit and integration tests for packages                                    |
| `tests/scripts/up.sh --verify` | Docker stack: vault nodes, Yggdrasil hubs, TLS edge, Go/Composer consumers |
| `tests/bench/run.sh`           | storage microbench: compression, cache, memtable, CGO                      |
| `tests/bench/highload.sh`      | live serving path under keepalive load                                     |

More: [tests](tests/README.md), [tests/bench](tests/bench/README.md).

### Generation

```bash
go generate .
```

Run exactly `go generate .`, not `go generate ./...`: the root `gen.go` runs generators in the required order.

| Artifact                     | Generator                        | Source               |
|------------------------------|----------------------------------|----------------------|
| `target/stconf`              | `goconfgen`                      | `yml/config`         |
| `target/stcode`              | local `_generate/{enums,errors}` | `yml/{enums,errors}` |
| `target/api`                 | `goopenapigen` / ogen            | `yml/openapi`        |
| `target/meta_gen.go`         | `gometagen`                      | `_run/values.yml`    |
| `target/dependencies_gen.go` | `godepsgen`                      | `go.mod`             |

After `go mod tidy`, run `go generate .` again so `target/dependencies_gen.go` matches the current `go.mod`.

### Module Map

| Module                                       | Responsibility                                                           |
|----------------------------------------------|--------------------------------------------------------------------------|
| [mod/cli](mod/cli/README.md)                 | argv parsing, presets, validation, keygen, maintenance, runtime start    |
| [mod/config](mod/config/README.md)           | YAML/JSON/HJSON loading, defaults, business validation                   |
| [mod/logger](mod/logger/README.md)           | console/file/VictoriaLogs logging                                        |
| [mod/server](mod/server/README.md)           | web/Ygg HTTP listeners, request frame, API handlers, static, brother RPC |
| [mod/route](mod/route/README.md)             | stable service routes and route constants                                |
| [mod/view](mod/view/README.md)               | HTML templates, CSS, UI pages, snippets                                  |
| [mod/mesh](mod/mesh/README.md)               | Yggdrasil node, identity, peers, dial/listen integration                 |
| [mod/telemetry](mod/telemetry/README.md)     | metric snapshots, JSON groups, Prometheus text, VictoriaMetrics push     |
| [mod/rescan](mod/rescan/README.md)           | periodic scan, fetch orchestration, publish flow                         |
| [mod/source](mod/source/README.md)           | git provider access, brother discovery/RPC/public fallback, egress guard |
| [mod/archive](mod/archive/README.md)         | safe unpacking of zip/tar/tar.gz release archives                        |
| [mod/overlay](mod/overlay/README.md)         | Go/Composer detection, Go rewrite, artifact planning/building            |
| [mod/storage](mod/storage/README.md)         | Pebble blobs, SQLite metadata, hot cache, quotas, maintenance            |
| [mod/cache](mod/cache/README.md)             | RAM byte-cache for small metadata responses                              |
| [mod/state](mod/state/README.md)             | runtime snapshots visible to serving paths                               |
| [mod/brotherwire](mod/brotherwire/README.md) | wire DTOs and protocol constants for brother RPC                         |
| [mod/core](mod/core/README.md)               | hashes, trees, artifacts, semver, shared value objects                   |
| [mod/internal](mod/internal/README.md)       | internal helpers such as filesystem wrappers                             |

The composition root is `main.go`: it calls `mod/cli.New()`, then CLI selects runtime, maintenance, or one-shot
commands. For contribution workflow and PR rules, see [CONTRIBUTING.md](CONTRIBUTING.md).

## Self Build

Most users should take a ready binary from [GitHub Releases](https://github.com/voluminor/yggvault/releases). Build it
yourself when you need a target without a release asset, custom `GOAMD64`, static CGO, custom `CGO_CFLAGS`, or local
changes.

Requirements:

- Go `1.26.3` or a compatible newer version;
- `git` and `bash` for source work;
- a C toolchain only for `CGO_ENABLED=1`;
- Docker only for containerized static CGO builds or the integration test stack.

Run the commands below inside a generated source archive from
[Getting Source from a Mirror](#getting-source-from-a-mirror), or inside a git checkout after [Bootstrap](#bootstrap).

Portable build uses the pure-Go SQLite driver and pure-Go codecs:

```bash
mkdir -p tmp
CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o tmp/yggvault .
```

`CGO_ENABLED=1` enables C SQLite and C zstd. It is useful when you choose `zstd`, `good`, or `balanced` for a large
write-heavy store:

```bash
mkdir -p tmp
CGO_ENABLED=1 go build -ldflags="-s -w" -trimpath -o tmp/yggvault-cgo .
```

| Build           | SQLite           | zstd    | Choose it for                                             |
|-----------------|------------------|---------|-----------------------------------------------------------|
| `CGO_ENABLED=0` | modernc, pure-Go | pure-Go | static binary, simple cross-compilation, `snappy`/`minlz` |
| `CGO_ENABLED=1` | mattn, C         | C       | denser zstd without a large CPU/RAM write penalty         |

### CGO Build for a Specific System

Local `CGO_ENABLED=1 go build` dynamically links against the build machine's glibc. For a portable CGO binary, build
statically on musl and choose the target CPU explicitly:

```bash
docker build -f tests/vault.Dockerfile \
  --build-arg GO_IMAGE=golang:1.26.3-alpine \
  --build-arg CGO_ENABLED=1 \
  --build-arg GOAMD64=v3 \
  --build-arg CGO_CFLAGS="-O2 -march=native" \
  --build-arg RUNTIME=alpine:3.20 \
  -t yrv:cgo-native . && \
cid=$(docker create yrv:cgo-native) && \
docker cp "$cid":/usr/local/bin/yggvault ./tmp/yggvault-cgo; \
docker rm "$cid" >/dev/null
```

Check the result: `ldd tmp/yggvault-cgo` should print `not a dynamic executable`.

- `GOAMD64=v3` requires a CPU with AVX2; use `v2` for older CPUs.
- `-march=native` tunes C code for the build host CPU. If the target is different, set the architecture explicitly.
- For `snappy`/`minlz`, CGO is usually unnecessary.

Disk, CPU, memory, and native/CGO build numbers are in [STORAGE-TUNING.md](STORAGE-TUNING.md).

## Project Policy

- License: GNU Lesser General Public License v2.1. See [LICENSE](LICENSE). If you need the exact SPDX interpretation
  for packaging, check the project notice before publishing downstream packages.
- Security reports: use [SECURITY.md](SECURITY.md). Do not open public issues or PRs for suspected vulnerabilities.
- Contributions: use [CONTRIBUTING.md](CONTRIBUTING.md). Development work should start from the git repository, not
  from stripped release or mirror source archives.
- Issues and support: include the version, commit, config fragments relevant to the problem, platform, and logs with
  secrets removed.
