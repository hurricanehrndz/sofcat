---
title: Developer notes
description: Tooling, build, lint and test workflow for working on SofCat itself.
---

## Environment

Tools come from [mise](https://mise.jdx.dev): `mise install` reads `mise.toml` and
provides Go 1.27, Node 24, `just`, `prek`, `golangci-lint`, `actionlint` with `shellcheck`, and
`treefmt` with `gofumpt` and `yamlfmt`. Activate mise in your shell (or prefix
commands with `mise exec --`).
`just dev-setup` installs the frontend dependencies and the [prek](https://prek.j178.dev)
pre-commit hook from `prek.toml`: repository hygiene checks, `treefmt`,
`golangci-lint` for Linux and Windows, and `actionlint`. `just pre-commit` runs
them on every file.

The Windows binaries cross-compile with no cgo, so `just build` needs nothing
else. Compiling or testing the `ui` package for the host (`just test`,
`just check-xplat`) does need cgo: on Linux install GTK4 and WebKitGTK 6 dev
packages plus `pkg-config` (Debian: `libgtk-4-dev libwebkitgtk-6.0-dev`), on
macOS the Xcode command line tools.

Run tasks with `just`: `just build`, `just test`, `just lint`, `just fmt`,
`just check-xplat`, `just makecatalogs`, `just clean` (see the `justfile`).
UI-specific recipes: `ui-lint` (TypeScript plus the
committed-binding check), `ui-test` (frontend tests), `ui-assets` (Vite
production build).

## Build artifacts

Every build recipe writes only under `build/` (gitignored), except the frontend
recipes: `ui-install` writes `ui/frontend/node_modules/` and `ui-assets`
writes `ui/frontend/dist/` (both gitignored), and `winres` (run by `build`)
writes `rsrc_windows_<arch>.syso` in `cmd/sofcat`, `cmd/sofcat-useragent` and `ui`
(gitignored) from `<pkg>/winres/winres.json`. Those carry the icon, a
VERSIONINFO resource stamped with `version`, and an `asInvoker` manifest into
all three executables. A plain `go build` without them gives an anonymous exe with
the generic icon, which antivirus ML heuristics trust less
([Code signing](/reference/code-signing/)). Nothing is emitted at the repo root.

A normal `just build` produces the three raw Windows executables:
`build/sofcat.exe`, `build/sofcat-ui.exe` and `build/sofcat-useragent.exe`,
the per-user helper. The UI build runs the Vite
production build first, then embeds `ui/frontend/dist`. No installer is
produced by `just build`; `just setup` builds the installer,
`build/sofcat-<version>-x86_64-setup.exe`, with embala's `setup` format (see
`installer/embala.toml` and the `install.lua` and `uninstall.lua` beside it).
Nothing is signed.

## Releases

Merging to `main` does not release. Push a version tag on a commit that is on
`main`: `vMAJOR.MINOR.PATCH` for a release, or `vMAJOR.MINOR.PATCH-beta.N` for
a preview, cut as a GitHub prerelease:

```sh
git fetch origin && git tag v5.1.0 origin/main && git push origin v5.1.0
```

Releases follow [semantic versioning](https://semver.org/) from v5.0.0, the
first stable release. A change that breaks config keys, the catalog format or
the service protocol needs a new major version, and its pull request says so
in the title or summary, since the release notes are generated from merged
pull requests. A preview may break anything the release it leads to has not
yet shipped.

Build Release (`.github/workflows/build_release.yml`) builds and signs
everything at that tag: on Linux the Windows executables, `setup.exe` and
makecatalogs, and on a macOS runner the arm64 CLI, helper, app and pkg. A
last job attaches the files below, with a signed provenance bundle
(`sofcat-<tag>.sigstore.json`), to the release with notes generated from the
merged pull requests, and the released-binary integration tests run on
Windows. The installer's file name and Add or remove programs entry carry only
`MAJOR.MINOR.PATCH`, so a preview's `setup.exe` is named like the release that
follows it. Once the release is cut, a last job asks the Site workflow to
deploy again, so the [Download](/download/) page lists it.

| File | Holds |
| --- | --- |
| `sofcat-<version>-x86_64-setup.exe` | The Windows installer. |
| `sofcat-<tag>-windows-amd64.zip` | `sofcat.exe`, `sofcat-ui.exe` and `sofcat-useragent.exe`. |
| `sofcat-<tag>.pkg` | The macOS installer, arm64, notarized. |
| `sofcat-<tag>-darwin-arm64.tar.gz` | `sofcat`, `sofcat-useragent` and `SofCat.app`. |
| `makecatalogs-<os>-<arch>.tar.gz`, `.zip` for Windows | One `makecatalogs` binary, for Linux, macOS and Windows on amd64 and arm64. |

`just archive-windows`, `just archive-macos` and `just makecatalogs-archives`
build the archives; the two that write a `.zip` need `zip`. It refuses a tag whose commit is not on `main`.

## Cross-compilation: pure Go, no cgo

The agent is pure Go — it has **zero cgo** (all Windows syscalls go through
`golang.org/x/sys/windows`), so the Windows agent cross-compiles from Linux with
plain `CGO_ENABLED=0 GOOS=windows go build` (`just build`). `just check-xplat`
proves the tree also still builds for `GOOS=linux`. There is **no zig and no cgo
toolchain** in this environment, and none is needed.

**Standing convention:** should any future component ever require cgo for OS
interfacing (e.g. a native MSI/registry reader we cross-distribute), `zig cc` is
the designated cross C compiler — wire it *there*, in that component's build
recipe, not here.
