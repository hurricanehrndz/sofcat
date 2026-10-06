# Developer notes

## Environment

Tools come from [mise](https://mise.jdx.dev): `mise install` reads `mise.toml` and
provides Go 1.26, Node 22, `just`, `golangci-lint`, `treefmt` with `gofumpt` and
`yamlfmt`. Activate mise in your shell (or prefix commands with `mise exec --`).
`just setup` installs the frontend dependencies and a pre-commit hook that runs
`just pre-commit` (format check plus incremental lint).

The Windows binaries cross-compile with no cgo, so `just build` needs nothing
else. Compiling or testing the `sofcat-ui` package for the host (`just test`,
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
recipes: `ui-install` writes `sofcat-ui/frontend/node_modules/` and `ui-assets`
writes `sofcat-ui/frontend/dist/` (both gitignored). Nothing is emitted at the
repo root.

A normal `just build` produces **both** raw Windows executables:
`build/sofcat.exe` and `build/sofcat-ui.exe`. The UI build runs the Vite
production build first, then embeds `sofcat-ui/frontend/dist`. No installer is
produced and nothing is signed.

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
