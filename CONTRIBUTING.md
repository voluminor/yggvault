# Contributing

Thanks for your interest in improving yggvault.

## Start from the development tree

Contribution work must start from `main` in the canonical GitHub repository. Fork `voluminor/yggvault` on GitHub
first, then clone your fork:

```bash
git clone https://github.com/<your-user>/yggvault.git
cd yggvault
```

Keep the canonical repository as `upstream`, base your branch on `upstream/main`, and then create a focused branch:

```bash
git remote add upstream https://github.com/voluminor/yggvault.git
git fetch upstream
git checkout -B main upstream/main
git checkout -b feat/short-descriptor
```

Do not base contribution work on release tags, GitHub release source archives, or mirrored release archives. The release
workflow in `.github/workflows/release.yml` creates stripped release tags for distribution: it embeds generated
`target/` and `tmp/` assets, then removes development, generator, test, configuration-source, workspace, and
documentation files such as `.github/`, `_generate/`, `_run/`, `tests/`, `yml/`, `gen.go`, `go.work`, `go.work.sum`,
`.gitignore`, and Markdown files. Those trees are useful for released builds, but they are not the full development tree
needed for pull requests.

## Requirements

- Go `1.26.3` or a compatible newer version.
- `git`, `bash`, and network access for Go modules and generator downloads.
- A C toolchain only when building with `CGO_ENABLED=1`.
- Docker and Compose only for the live integration test stack.

Check the basic tools before bootstrapping:

```bash
go version
git --version
bash --version
```

## Bootstrap

The recommended first setup on Linux is:

```bash
bash _run/firststart.sh
```

The script creates `target/` and `tmp/`, installs the project generators, installs the git hooks, runs `go work sync`,
runs `go generate .`, tidies all main modules, and runs `go generate .` again.

If you do not want the hooks installed automatically, use the manual setup:

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

Run exactly `go generate .`, not `go generate ./...`: the root generator entrypoint orders the generators correctly. Do
not keep hand-written files in `target/` or `tmp/`; generation may clean those directories.

## Build

Portable pure-Go build:

```bash
CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o tmp/yggvault .
```

CGO build with the C SQLite driver and C zstd path:

```bash
CGO_ENABLED=1 go build -ldflags="-s -w" -trimpath -o tmp/yggvault-cgo .
```

For most local development, the pure-Go build is enough.

## Run locally

Create a minimal configuration, edit `release_mirrors` to point at a real upstream, then validate and start the server:

```bash
./tmp/yggvault --make-preset minimal --out .
./tmp/yggvault --validate-config config.yml
./tmp/yggvault config.yml
```

The server will fetch upstream releases during rescan and publish the configured keys through the web, package-manager,
and metrics routes.

## Test

Run the package test suite before opening a pull request:

```bash
go test ./...
```

Use the live stack when touching Yggdrasil, HTTP serving, source fetching, storage, package-manager overlays, or
integration behavior:

```bash
bash tests/scripts/up.sh --verify
```

Benchmarks are useful for storage, cache, compression, and high-load serving changes:

```bash
REPS=3 bash tests/bench/run.sh
CONC=200 DUR=20 bash tests/bench/highload.sh
```

After changing generator inputs, OpenAPI/config YAML, `go.mod`, or `go.sum`, run `go generate .` again and include the
resulting generated changes when they belong to the change. Do not edit generated files directly.

## Commits and pull requests

- Use clear imperative commit subjects, for example `fix: reject oversized archive entries`.
- Keep pull requests focused on one problem.
- Describe what changed, why it changed, and how it was tested.
- Link related issues when there are any.
- Update README or module documentation when public behavior, configuration, routes, or operator workflows change.
- Include screenshots or viewport notes for web UI changes.
- Keep generated output and dependency changes explainable.
- Call out high-load, resource-exhaustion, malicious-input, and abuse considerations when they are relevant to the
  change.

CI tests the project on Linux, macOS, and Windows, then builds the release matrix for pure-Go and CGO binaries. All
relevant checks should pass before review.

## Reporting issues

Please include:

- steps to reproduce;
- expected and actual behavior;
- Go version, OS, architecture, and build mode (`CGO_ENABLED=0` or `CGO_ENABLED=1`);
- relevant configuration with secrets removed;
- logs or command output when they help explain the failure.

Report security issues privately using `SECURITY.md`.

## Licensing

This project is licensed under the GNU Lesser General Public License, Version 2.1. By submitting a contribution, you
agree that it will be licensed under those terms.
