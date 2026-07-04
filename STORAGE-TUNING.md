# Storage and serving tuning

This guide helps you choose storage, cache, memory, verification, and build settings for a `yggvault` node. Start with
the workload, then check the benchmark numbers near the end.

For installation, first run, Yggdrasil setup, source archives, and release binaries, start with
[README.md](README.md). This file only covers storage and serving trade-offs.

## Quick Choice

| Task now                         | Baseline profile                                          | Build                  | Main gain                                  | Main cost                                   |
|----------------------------------|-----------------------------------------------------------|------------------------|--------------------------------------------|---------------------------------------------|
| Run a reliable ordinary node     | [Default minlz profile](#default-minlz-profile)           | `CGO_ENABLED=0`        | good disk use, simple deployment           | not minimum disk                            |
| Maximize portable compatibility  | [Portable compatibility](#portable-compatibility)         | `CGO_ENABLED=0`        | fewer CPU/runtime surprises, static binary | slightly more disk than `minlz`             |
| Minimize durable storage         | [Minimum durable disk](#minimum-durable-disk)             | `CGO_ENABLED=1`        | densest Pebble store                       | C toolchain or container build              |
| Save disk but avoid CGO          | [Large store without CGO](#large-store-without-cgo)       | `CGO_ENABLED=0`        | stronger compression on deep LSM stores    | weak effect on small stores                 |
| Ingest many new versions         | [Write-heavy ingest](#write-heavy-ingest)                 | `CGO_ENABLED=0` or `1` | fewer flushes/write amplification          | higher baseline RSS                         |
| Run with little RAM              | [RAM saving](#ram-saving)                                 | `CGO_ENABLED=0`        | lower memory floor                         | less parallelism, more rebuilds             |
| Serve large artifacts repeatedly | [Read-heavy artifacts](#read-heavy-artifacts)             | any                    | lower serving latency                      | weaker hot-file verification, more hot disk |
| Prefer integrity over latency    | [Integrity-critical serving](#integrity-critical-serving) | any                    | verifies every hot response                | latency grows with artifact size            |

Main rule: tune for the current bottleneck. Do not enable `zstd`, a large `block_cache`, `memtable=256mb`, and
`hot.verify_on_read=always` all at once "just in case"; these knobs solve different problems.

## Build Prerequisites for Examples

The `go build` snippets below assume one of these source layouts:

- a release or mirror source archive, which already contains generated files and can be built directly;
- a git checkout after the bootstrap from [README.md#bootstrap](README.md#bootstrap).

For source archive workflow and the public mirrors, see
[README.md#getting-source-from-a-mirror](README.md#getting-source-from-a-mirror). For full build context, see
[README.md#self-build](README.md#self-build).

All local examples create `tmp/` before writing binaries:

```bash
mkdir -p tmp
```

## Profiles with Ready Config Fragments

The fragments below can be pasted into `config.yml`. They are not complete configs; they only set the storage knobs
that matter for the profile.

### Default minlz profile

Choose this for a normal public or private mirror node when you do not have a measured bottleneck. This is the
practical default: portable, predictable, and free of C runtime dependencies.

```yaml
storage:
  pebble:
    compression: minlz
    block_cache_size: 128mb
    memtable_size: 64mb
    verify_on_read: always
  hot:
    max_size: 2gb
    idle_ttl: 1h
    retain: latest
    verify_on_read: sampled
```

Build:

```bash
mkdir -p tmp
CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o tmp/yggvault .
```

Expected result on the reference text dataset: durable storage was about 62% smaller than `none`, with almost no
deployment complexity.

### Portable compatibility

Choose this when a static binary, simple cross-compilation, weak CPU, or predictable behavior across architectures is
more important than the last few percent of disk usage.

```yaml
storage:
  pebble:
    compression: snappy
    block_cache_size: 128mb
    memtable_size: 64mb
    verify_on_read: always
  hot:
    verify_on_read: sampled
```

Build:

```bash
mkdir -p tmp
CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o tmp/yggvault .
```

Trade-off: `snappy` usually uses a little more disk than `minlz`, but it remains the simplest low-CPU option. On arm64,
`minlz` may effectively be equal to `snappy`, so this profile often loses almost nothing to the default profile.

### Minimum durable disk

Choose this when durable disk is more expensive than CPU and you can build with CGO. This is the best profile for large
archival mirrors where the write path should not pay the pure-Go zstd penalty.

```yaml
storage:
  pebble:
    compression: zstd
    block_cache_size: 128mb
    memtable_size: 64mb
    verify_on_read: always
  hot:
    verify_on_read: sampled
```

Build:

```bash
mkdir -p tmp
CGO_ENABLED=1 go build -ldflags="-s -w" -trimpath -o tmp/yggvault-cgo .
```

For a production binary that does not depend on the build host glibc, use the static musl container build in
[Build: portable, CGO, and native target](#build-portable-cgo-and-native-target).

Expected result on the reference text dataset: `zstd` gave about -70% vs `none` and about -25% vs `snappy` on disk.
The main `CGO_ENABLED=1` win here is not size; it removes most of the pure-Go zstd CPU/RAM write penalty.

### Large store without CGO

Choose this when the store is large, disk matters, but a C toolchain or CGO build is unavailable. On a small store,
this profile is close to `minlz`/`snappy`; it becomes more useful on a deep LSM.

```yaml
storage:
  pebble:
    compression: balanced
    block_cache_size: 128mb
    memtable_size: 64mb
    verify_on_read: always
  hot:
    verify_on_read: sampled
```

Build:

```bash
mkdir -p tmp
CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o tmp/yggvault .
```

`balanced` is valid without CGO. CGO becomes interesting for `balanced` only when write cost matters on large or deep
LSM stores. If write CPU/RAM is already the bottleneck, compare this profile against `zstd` with `CGO_ENABLED=1`.

### Write-heavy ingest

Choose this when the node often imports new versions, rewrites metadata, or builds many overlays.

```yaml
source:
  download_max_parallel: 4

rescan:
  max_parallel_keys: 2

storage:
  overlay_build_max_parallel: 4
  in_flight_read_bytes: 512mb
  pebble:
    compression: minlz
    block_cache_size: 128mb
    memtable_size: 256mb
    verify_on_read: always
  hot:
    max_size: 4gb
    retain: latest
    verify_on_read: sampled
```

Expected result: fewer flushes and less write amplification. Cost: higher baseline RSS. Do not use this on a small VPS
without checking memory headroom.

### RAM saving

Choose this for small nodes where the first limit is memory, not disk or raw throughput.

```yaml
source:
  download_max_parallel: 2

rescan:
  max_parallel_keys: 1

storage:
  overlay_build_max_parallel: 1
  in_flight_read_bytes: 64mb
  pebble:
    compression: snappy
    block_cache_size: 8mb
    memtable_size: 16mb
    verify_on_read: always
  hot:
    max_size: 512mb
    idle_ttl: 30m
    retain: latest
    verify_on_read: sampled

cache:
  metadata_max_size: 16mb
```

This profile reduces the memory floor, but it gives up some parallelism and repeated-read cache. The README also has an
even more aggressive low-RAM fragment that sets `hot.retain: none`.

### Read-heavy artifacts

Choose this when clients repeatedly download the same large `.zip`/`.tar.gz` artifacts.

```yaml
storage:
  hot:
    max_size: 20gb
    idle_ttl: 24h
    retain: all
    verify_on_read: sampled
```

Expected result: fewer rebuilds from durable storage and lower latency for hot artifacts. `sampled` remains the public
default because it keeps some hot-file integrity checking without paying the full cost on every response.

If the hot cache is on trusted local storage and latency is more important than hot-file body verification, you can use:

```yaml
storage:
  hot:
    verify_on_read: never
```

`never` disables the BLAKE3 body hash on hot-file opens. Path/type/size checks and durable rebuild validation still
exist, but a corrupted hot file body may be served until the file is rebuilt or an external check catches it.

### Integrity-critical serving

Choose this when every served hot artifact must be body-verified on read.

```yaml
storage:
  hot:
    verify_on_read: always
```

Expected result: strongest hot-file verification. Cost: latency and CPU grow roughly linearly with artifact size.

## Combining Profiles

- `storage.pebble.compression` affects durable storage size and write cost. It barely changes latency once a hot file
  already exists.
- `storage.hot.verify_on_read` affects the client-serving path. On read-heavy nodes it can matter more than
  `compression`, because hashing the response body is paid on every served hot artifact.
- `storage.pebble.memtable_size` and `storage.pebble.block_cache_size` set the baseline RAM floor. Do not increase both
  unless you know whether write-heavy ingest or repeated durable reads are the bottleneck.
- `storage.in_flight_read_bytes` does not speed the system up by itself. It is backpressure against OOM during
  concurrent blob materialization.
- `storage.hot.max_size`, `idle_ttl`, and `retain` decide how many ready artifacts stay on disk. A small hot cache
  saves disk but moves cost to CPU rebuilds.
- Disk-oriented compression profiles affect durable storage. Total node disk also depends on `storage.hot.*`,
  `storage.quota.*`, logs, and your source archive set.

## What Each Knob Changes

| Parameter                         | What it changes                                              | What it does not solve                             |
|-----------------------------------|--------------------------------------------------------------|----------------------------------------------------|
| `storage.pebble.compression`      | durable disk size, write CPU/RAM for zstd-family codecs      | hot-file latency after the file is built           |
| `CGO_ENABLED`                     | zstd codec and SQLite driver                                 | little effect on `snappy`, `minlz`, `fast`, `none` |
| `storage.pebble.memtable_size`    | write amplification, flush frequency, write RAM floor        | hot cache size                                     |
| `storage.pebble.block_cache_size` | repeated durable reads, decompression cache                  | first read and streaming an existing hot file      |
| `storage.in_flight_read_bytes`    | peak RAM during concurrent build/read                        | throughput if the limit is not reached             |
| `storage.hot.max_size`            | how often ready artifacts are rebuilt                        | durable storage size                               |
| `storage.hot.idle_ttl`            | how quickly idle hot files are removed                       | durable storage integrity                          |
| `storage.hot.retain`              | which versions stay ready                                    | whether the version exists in durable storage      |
| `storage.hot.verify_on_read`      | latency and CPU on client serving                            | blob verification during rebuild                   |
| `storage.pebble.verify_on_read`   | durable blob/tree verification during build and repair paths | hot serving path                                   |

## Benchmark Numbers and Comparisons

All numbers below are `median-of-3` on one reference machine:

- amd64;
- 8 cores;
- loopback;
- default build `CGO_ENABLED=0`, unless stated otherwise;
- storage: Pebble + SQLite;
- ready artifacts live in the regenerable hot cache;
- main text dataset for the compression table: 26 MB.

Absolute `req/s`, MB/s, and compression percentages depend on hardware and data. Relative ordering is more stable than
absolute numbers.

Raw result JSON/TXT files are not committed in this repository. For publishable sizing, rerun the benchmarks on your
target machine and record at least: commit hash, CPU model, OS/kernel, Go version, storage medium, dataset source, and
raw result files.

### Compression: Durable Storage Size and Write Cost

Measurement: `CGO_ENABLED=0`, reference text dataset 26 MB.

| compression         | on disk | vs `none` | vs `snappy` | write CPU   | write RAM |
|---------------------|---------|-----------|-------------|-------------|-----------|
| `none`              | 26.5 MB | x1.00     | +149%       | low         | low       |
| `snappy`            | 10.6 MB | -60%      | baseline    | low         | low       |
| `minlz`             | 10.0 MB | -62%      | -5%         | low         | low       |
| `fast` / `fastest`  | 10.0 MB | -62%      | -5%         | low         | low       |
| `balanced` / `good` | 10.0 MB | -62%      | -5%         | low*        | low*      |
| `zstd`              | 8.0 MB  | -70%      | -25%        | higher x1.9 | higher x5 |

`*` On the small store. On a large store, `balanced`/`good` are more expensive because stronger compression starts
being applied on cold lower LSM levels.

Practical reading:

- `none`: use only for debugging or almost incompressible data.
- `snappy`: compatibility and low CPU baseline.
- `minlz`: current default; on amd64 it gave about -5% vs `snappy` almost for free.
- `fast`/`fastest`: same practical class as `minlz`/`snappy` on the reference dataset.
- `zstd`: minimum disk, but expensive on writes under `CGO_ENABLED=0`.
- `balanced`/`good`: do not expect a big win on small stores; they are for larger and deeper LSM stores.

### CGO: Effect on zstd and SQLite

| Build           | zstd codec | zstd write CPU/RAM                               | SQLite driver                        |
|-----------------|------------|--------------------------------------------------|--------------------------------------|
| `CGO_ENABLED=0` | pure-Go    | expensive: about x1.9 CPU and x5 RAM vs `snappy` | modernc, pure-Go, static binary      |
| `CGO_ENABLED=1` | C          | close to `snappy`; main penalty removed          | mattn, C, slightly faster index path |

Conclusion: CGO is usually unnecessary for `snappy`/`minlz`. It matters for `zstd`/`good`, and can matter for
`balanced` on large write-heavy stores.

### RAM: Memtable, Block Cache, In-Flight

| Knob                                 | Value            | Effect                                       |
|--------------------------------------|------------------|----------------------------------------------|
| `memtable_size=16mb`                 | tight RAM        | higher write CPU, lower RAM floor            |
| `memtable_size=64mb`                 | default          | balanced                                     |
| `memtable_size=256mb`                | write-heavy      | about -15% CPU vs 16 MB, but higher RSS      |
| `block_cache_size=8-16mb`            | tight RAM        | less cache memory, higher repeated read cost |
| `block_cache_size=128mb`             | default          | normal repeated durable read profile         |
| `in_flight_read_bytes` below default | OOM backpressure | serializes concurrent build/read earlier     |

Memtable size should be proportional to large blob objects. If `storage.archive_limits.size.per_file` is near 64 MB,
do not set `memtable_size=16mb` without intentionally limiting file size.

### Hot Verify: Serving Latency

This measures `storage.hot.verify_on_read`, because it sits on the hot serving path.

| Artifact size | `always` | `sampled` (1/16) | `never` |
|---------------|----------|------------------|---------|
| 64 KiB        | 0.14 ms  | 0.05 ms          | 0.04 ms |
| 1 MiB         | 1.84 ms  | 0.20 ms          | 0.04 ms |
| 16 MiB        | 35 ms    | 2.2 ms           | 0.02 ms |

Takeaways:

- `never` removes the hot-file BLAKE3 body hash from each response and gives almost constant tens of microseconds.
- `sampled` checks 1 out of 16 opens and is roughly 15 times cheaper than `always` on large files.
- `always` is for integrity-critical serving; its cost grows linearly with artifact size.

### High-Load Serving

Measurement: live server, keepalive, 200 concurrent connections, loopback, reference dataset.

| Profile                            | Served object    | Achieved              | Latency p50 / p99 | Node CPU        | Node RAM |
|------------------------------------|------------------|-----------------------|-------------------|-----------------|----------|
| Artifact about 192 KB, `never`     | `.zip` to client | 4,677 req/s, 900 MB/s | 41 / 90 ms        | about 4.6 cores | 38 MB    |
| Artifact about 192 KB, `sampled`   | `.zip` to client | 4,474 req/s, 861 MB/s | 42 / 95 ms        | about 4.7 cores | 40 MB    |
| Artifact about 192 KB, `always`    | `.zip` to client | 4,096 req/s, 788 MB/s | 47 / 101 ms       | about 4 cores   | 47 MB    |
| Metadata `catalog.json`, cache hit | small JSON       | 32,193 req/s          | 5 / 21 ms         | 0.2%            | 38 MB    |

Takeaways:

- With an artifact around 192 KB, the node served about 4.1-4.7k req/s and 788-900 MB/s.
- `hot.verify_on_read` is visible even at 192 KB: `never -> sampled -> always` cost about -4% and -12% vs `never`.
- On MiB artifacts, the gap grows because hash cost is linear.
- Metadata cache path is cheap compared with artifact serving: 32,193 req/s, p50/p99 5/21 ms.
- RAM stayed stable: 38-47 MB under 200 connections.
- Errors in all runs: 0.

### Build: Portable, CGO, and Native Target

Build choice should follow the storage profile.

| Build                                 | Choose it when                                          | What improves                                      |
|---------------------------------------|---------------------------------------------------------|----------------------------------------------------|
| `CGO_ENABLED=0`                       | portable binary, simple deploy, `snappy`/`minlz`/`fast` | static build, fewer external requirements          |
| `CGO_ENABLED=1`                       | `zstd`, `good`, or `balanced` when write cost matters   | C zstd removes the main write CPU/RAM penalty      |
| `GOAMD64`/`CGO_CFLAGS` for target CPU | CPU-bound node on known hardware                        | Go code and C SQLite/zstd can use CPU instructions |

Portable baseline:

```bash
mkdir -p tmp
CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o tmp/yggvault .
```

Generic local CGO:

```bash
mkdir -p tmp
CGO_ENABLED=1 go build -ldflags="-s -w" -trimpath -o tmp/yggvault-cgo .
```

Native/static container build for a specific Linux host:

```bash
mkdir -p tmp
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

Check the result:

```bash
ldd tmp/yggvault-cgo
```

For a static musl binary, `ldd` should print `not a dynamic executable`.

What counts as a win:

- For `zstd`/`good`, the largest measured win is `CGO_ENABLED=1`: pure-Go zstd write cost was about x1.9 CPU and x5
  RAM vs `snappy`; C zstd almost removed that penalty.
- For `snappy`, `minlz`, `fast`, `fastest`, and `none`, CGO is usually unnecessary for storage performance.
- `GOAMD64=v3` and `CGO_CFLAGS="-O2 -march=native"` can help CPU-bound profiles, but there is no universal percentage.
  If the bottleneck is disk, network, or client download speed, native build may do almost nothing.
- Native binaries are tied to CPU class. `GOAMD64=v3` requires AVX2/BMI2/FMA. `-march=native` also tunes C code for the
  build host CPU; do not run that binary on older CPUs unless you know the instruction set is compatible.
- For portable production artifacts, use the baseline build or build separate binaries per host class.

## How to Reproduce

Storage microbench. By default, the wrapper uses `$HOME/go/pkg/mod`, caps the raw dataset at 150 MB, builds CGO=0 and
CGO=1 images, and writes JSON to `tmp/tests/bench-results`:

```bash
DATASET="$HOME/go/pkg/mod" RAW_CAP_MB=150 REPS=3 bash tests/bench/run.sh
```

Result files:

- `tmp/tests/bench-results/storage-nocgo.json`;
- `tmp/tests/bench-results/storage-cgo.json`.

Latency by `verify_on_read` and artifact size:

```bash
go test -run='^$' -bench=BenchmarkHotVerifyOnRead -benchmem ./mod/storage/
```

High-load serving:

```bash
CONC=200 DUR=20 bash tests/bench/highload.sh
```

Result file:

- `tmp/tests/bench-results/highload.txt`.

Benchmark harness description: [tests/bench](tests/bench/README.md).

## Interpretation Limits

- One host.
- One tenant.
- Loopback.
- Reference text dataset.
- Storage and serving numbers were measured on a specific machine.
- Raw result files are not part of committed source; if you compare changes, save JSON/TXT files next to the commit
  hash.

For production sizing, benchmark on the target machine, with the target release archive set, and with an external load
generator when you need to include network, TLS, and reverse proxy behavior. For native builds, compare at least three
variants: portable `CGO_ENABLED=0`, generic `CGO_ENABLED=1`, and target-tuned build with `GOAMD64`/`CGO_CFLAGS`.

## Related Project Documents

- Main user guide and release assets: [README.md](README.md)
- Self build and source archive workflow: [README.md#self-build](README.md#self-build)
- Security policy: [SECURITY.md](SECURITY.md)
- Contribution workflow: [CONTRIBUTING.md](CONTRIBUTING.md)
- License: [LICENSE](LICENSE)
