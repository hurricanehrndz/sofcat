![SofCat logo](sofcat.png)
# SofCat [![Go Report Card](https://goreportcard.com/badge/github.com/hurricanehrndz/sofcat)](https://goreportcard.com/report/github.com/hurricanehrndz/sofcat) [![Build status](https://github.com/hurricanehrndz/sofcat/actions/workflows/go-test.yml/badge.svg?branch=main)](https://github.com/hurricanehrndz/sofcat/actions/workflows/go-test.yml)

Munki-like Application Management for Windows

SofCat is a fork of [Gorilla](https://github.com/1dustindavis/gorilla) that provides application management on Windows using [Munki](https://github.com/munki/munki) as inspiration.
SofCat supports `.msi`, `.ps1`, `.exe`, or `.nupkg` [(via chocolatey)](https://github.com/chocolatey/choco).

## Getting Started
Information related to installing and configuring SofCat can be found on the upstream [Gorilla wiki](https://github.com/1dustindavis/gorilla/wiki); the config keys are the same.
For quick manual-test setup helpers on a fresh Windows VM, see [test/manual/README.md](test/manual/README.md).

## Building

If you just want the latest version, download it from the [releases page](https://github.com/hurricanehrndz/sofcat/releases).

Building from source needs Go 1.26, Node 22 and `just`. [mise](https://mise.jdx.dev)
installs all of them plus the lint and format tools from `mise.toml`: run
`mise install`, then `just setup` once. See [docs/dev.md](docs/dev.md).

`just build` produces **both** raw Windows executables in
`build/`:

- `build/sofcat.exe` — the agent/CLI/service
- `build/sofcat-ui.exe` — the Wails self-service UI (pure Go, no cgo)

## Install layout

- `C:\Program Files\SofCat\` holds `sofcat.exe` and `sofcat-ui.exe`.
- `C:\ProgramData\SofCat\` holds `config.yaml`, the cache, the log, the
  self-serve manifest and `inventory.json` (`app_data_path`; see
  [docs/data-directory.md](docs/data-directory.md) for its ACL).

`just msi` wraps them in `build/sofcat-<version>-x86_64.msi` with
[embala](https://github.com/hurricanehrndz/embala), built on any host without
WiX. The MSI installs both executables and registers the `sofcat` service; it
ships no `config.yaml`. Releases publish the two executables, the MSI and the
standalone `makecatalogs` binaries (see below); nothing is code signed.

UI-specific targets: `just ui-lint` (TypeScript and generated-binding check),
`just ui-test` (frontend tests). See [ui/README.md](ui/README.md).

## Contributing
Pull Requests are always welcome. Before submitting, lint and test:
```
go fmt ./...
go test ./...
```

## Building Catalogs (makecatalogs)
`makecatalogs` compiles a repo's `packages-info/*.yaml` files into
`catalogs/<catalog>.yaml`, like Munki's `makecatalogs`. It is a standalone, pure-Go
binary for Linux, macOS and Windows, so a package repo's CI never needs Windows.
It reads no SofCat config file.

```
makecatalogs [--check] <repo_path>
```

- Without `--check`, it replaces `<repo_path>/catalogs/`. A package-info file
  with no `catalog`, a catalog name containing a path, or one that differs from
  another only by case is skipped with a warning; a duplicate item name within
  a catalog warns and the later file wins. If nothing is left to write, it
  fails and leaves `catalogs/` alone. Dotfiles such as `._foo.yaml` are ignored.
- `--check` validates the repo, prints what it would write, and writes nothing.
  It fails on those warnings too, so use it as a pull-request gate in a package
  repo.
- Exit codes: `0` success, `1` any error (or any problem under `--check`),
  `2` usage error.

Releases publish `makecatalogs-<os>-<arch>[.exe]` for linux, darwin and windows
on amd64 and arm64. `just makecatalogs` builds the same set into `build/`.
See `examples/example_package-info.yaml` for a package-info example.

`sofcat -build`/`-b` and `-import`/`-i` were removed, along with the
`repo_path` config key; existing configs that still set it keep loading.
`-import` was never implemented.
