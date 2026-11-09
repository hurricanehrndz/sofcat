# AGENTS.md

Guidance for coding agents working in this repository.

## Scope

- Applies to the entire repository unless a deeper `AGENTS.md` overrides it.

## Project Context

- Project: `sofcat` (Windows-focused application/package management tool in Go).
- Main entrypoint: `./cmd/sofcat`.
- SofCat deploys to Windows and macOS. Windows is first-class (SYSTEM service plus named pipe); macOS is a launchd daemon plus an app bundle.
- CI runs in GitHub Actions and primarily targets Windows (`windows-latest`) to match deployment expectations.
- Development often happens on macOS: keep macOS build/test/dev workflows working where practical, but do not add major complexity solely to preserve parity.
- Where appropriate, macOS/non-Windows stub or no-op behavior is acceptable if it keeps development workflows usable.

## Preferred Workflow

1. Read relevant package(s) before editing.
2. Make minimal, focused changes.
3. Run broad local validation with `just test` (tests are fast/lightweight in this repo).
4. Keep changes ready for PR review (clear commits, no unrelated edits).
5. For each task, create and use a new branch named `agent/<task-slug>` (do not work on `main`).
6. Before any commit, verify the current branch was created by this agent for this task; if not, stop and create a new `agent/<task-slug>` branch.
7. Do not commit to or push a branch you did not create unless explicitly asked to.

## Build & Test Commands

- Helpful just recipes (`just` alone lists them all):
  - `just build`
  - `just test`
  - `just ui-lint`
  - `just ui-test`
  - `just clean`
  - `just bootstrap`
  - `just bootstrap-run`
  - `just test-zip-only`: Go tests under the `zip_only` tag
  - `just bootstrap-tapas`: build the Tapas Test zip-item fixture brand and repository for a Windows VM
  - `just bootstrap-tapas-macos`: the same for a Mac, with a `.pkg` and config
  - `just bootstrap-nupkg`: build `sofcat.exe` and an embala-built nupkg repository for the nupkg smoke on a Windows VM
  - `just build-macos`: build the CLI and `.app` for the host Mac
  - `just pkg`: wrap those into an unsigned `.pkg`

`just build-macos`, `just pkg` and `just bootstrap-tapas-macos` run only on macOS.

Prefer `just test` as the default local validation step, even for small changes.
When changes include SofCat UI code, run `just ui-lint` and `just ui-test`.
When changes span Go service/CLI and UI protocol layers, run `just test`, `just ui-lint`, and `just ui-test`.

`just build` produces the three raw Windows executables, all pure Go,
cross-built (`GOOS=windows GOARCH=amd64 CGO_ENABLED=0`) and stripped
(`-ldflags "-s -w"`): `build/sofcat.exe`, the windows-GUI storefront
`build/sofcat-ui.exe`
(`go build -tags production -ldflags "-s -w -H windowsgui" ./ui`) and the
per-user helper `build/sofcat-useragent.exe` (`cmd/sofcat-useragent`, also
`-H windowsgui`, no Wails).
`just setup` wraps them in embala's `setup.exe` (`installer/embala.toml`,
`install.lua` and `uninstall.lua`, embala 0.10.0); it builds on Linux. The
installer copies them into `C:\Program Files\SofCat`, adds the Start-menu
shortcut and the Add or remove programs entry, then runs
`sofcat.exe -serviceinstall`, which registers and starts the service and
writes the HKLM `Run` value `<slug>-user-sync` that starts the per-user
helper, `<slug>-useragent.exe`, at logon. The service also starts the helper
in every signed-in session whenever the user-placement plan changes. Its
`uninstall.exe` runs `sofcat.exe -serviceremove`, which stops and deletes
the service, removes the value and ends any running helper, then reverses
the copies. sofcat owns both on every platform, so
the installer scripts only call it. `setup.exe /S` and `uninstall.exe /S` are
the unattended forms; `setup.exe` refuses to install over an existing service.
With `SIGN_CMD` set (environment or `just SIGN_CMD=... setup`), `build` runs
it on the three executables and `setup` has embala run it on `setup.exe` and the
uninstaller stub (`site/src/content/docs/reference/code-signing.md`); unset,
nothing is signed.
Everything the service writes lives under `C:\ProgramData\SofCat`, as under
`/Library/Application Support/<Name>` on a Mac: `config.yaml`, the `Data`
directory (the default `app_data_path`, SYSTEM and Administrators only
except `Data\user-artifacts`, which Users read) and any placement of its own
(`site/src/content/docs/reference/data-directory.md`).
A brand with `INSTALLERS=zip` in its `brand.env` adds the `zip_only` tag to all
three binaries, which compiles out the command installers and catalog scripts
(`*_command.go`/`*_script.go` versus their `*_zip_only.go` twins); test that
set with `just test-zip-only` and
`golangci-lint run --build-tags zip_only ./...`.

On a Mac, `just build-macos` builds the CLI for the host architecture
(`build/<slug>`, pure Go) and the UI as `build/<Name>.app` (cgo, so the Xcode
command line tools), and `just pkg` wraps both with the launchd plist into
`build/<slug>-<version>.pkg`, unsigned by default. With `CODESIGN_IDENTITY` set,
`build-macos` codesigns the CLI and the app (hardened runtime; `-` is
ad-hoc), with `PRODUCTSIGN_IDENTITY` set `pkg` signs the product archive, and
with `NOTARIZE=1` plus `NOTARY_AUTH=api-key`, `NOTARY_API_KEY_PATH`,
`NOTARY_API_KEY_ID` and `NOTARY_API_ISSUER_ID` it runs `just notarize` on it
(`site/src/content/docs/reference/code-signing.md`). It installs the app in `/Applications`,
the CLI in `/Library/Application Support/<Name>/bin/<slug>` and the plist,
labelled with the brand's `IDENTIFIER`, in `/Library/LaunchDaemons`; its
postinstall script bootstraps the daemon. The daemon reads
`/Library/Application Support/<Name>/config.yaml`, keeps its state in the
root-only `.../<Name>/Data` (0700) and serves
`/var/run/<slug>/<slug>-service.sock`. The service and `-serviceinstall` make
the config root-owned and 0600, since it can hold `auth_pass`.
`<slug> -serviceinstall|-servicestart|-servicestop|-serviceremove` drive
`launchctl` as the SCM actions do on Windows.

The darwin lint from Linux is `GOOS=darwin golangci-lint run ./pkg/... ./cmd/...`:
`GOOS=darwin golangci-lint run ./...` cannot typecheck `ui/` there, because
Wails needs cgo on macOS. On the Mac itself, `just lint` covers the whole tree.

## Code Style

- Use idiomatic Go and keep code gofmt-clean.
- Prefer small, explicit functions over broad refactors.
- Preserve existing package boundaries (`cmd/`, `pkg/`, `test/`, `installer/`).
- Keep SofCat UI code and related docs under `ui/` unless there is a clear reason to place files elsewhere.
- Do not add new dependencies unless necessary, and explicitly call out/review any dependency additions in the PR.

## Windows & Integration Notes

- Be careful with path handling, newlines, and shell behavior differences.
- Changes that affect service behavior should include/adjust tests in `pkg/service` and `cmd/sofcat` when appropriate.
- When changing Windows named-pipe/service code paths, add or update Windows-only tests (`//go:build windows`) and validate on a Windows VM.
- Keep diagnostics pragmatic: prefer debug-level logging or explicit debug toggles over always-on high-volume tracing.
- Manual/integration helpers live under:
  - `test/integration/`
  - `test/manual/`

## Config & Examples

- If behavior/config changes, update examples and tests together:
  - `examples/example_config.yaml`
  - `examples/example_catalog.yaml`
  - `examples/example_manifest.yaml`
  - `examples/example_package-info.yaml`
- For UI protocol/data shape changes, keep the JSON contract aligned with the YAML model represented in:
  - `examples/example_manifest.yaml`
  - `examples/example_package-info.yaml`

## UI & Protocol Notes

- SofCat UI is a Wails v3 application (`package main` in `ui/`) inside
  this repository's root Go module. Its frontend is vanilla TypeScript + Vite.
- Local tooling prerequisites: `mise install` (see `mise.toml`) supplies Go, Node 24,
  `just` and the lint and format tools. No other SDK is needed; the shipped Windows
  binary is a pure-Go cross-build. Native Linux builds of `ui/` need GTK 4 and
  WebKitGTK 6.0; on nix, mise sources `nix-env.sh`, which loads `flake.nix` for
  exactly those, and the toolchain stays in mise.
- Generated Wails TypeScript bindings under `ui/frontend/bindings/` are
  committed and must never be hand-edited. Regenerate them from `ui/` with
  the pinned command and commit the result:

      go run github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0 generate bindings -clean -ts -noevents -d frontend/bindings .

  `just ui-lint` fails when the committed tree and a fresh generation differ.
- The service speaks JSON-RPC 2.0 (newline-delimited, one request per connection, plus
  the `streamOperationStatus` notification stream); the contract is the "Protocol"
  section of `ui/ARCHITECTURE.md`. Only `pkg/service/transport_*.go` is per
  platform, so keep protocol and runner code portable and its tests running on Linux.
- Keep `cmd/sofcat` service-message commands updated in lockstep with SofCat UI protocol changes for testing/debugging.
- `ListOptionalInstalls` should return JSON-safe subset DTOs, not full internal item objects.
- The bound Wails surface is exactly six methods, `ListOptionalInstalls`, `InstallItem`,
  `RemoveItem`, `WatchOperation`, `GetBranding`, and `CancelOperation`, and one
  `sofcat:operation-status` event; the service's `getServiceInfo` is not bound.
  Progress percentages are per item, not aggregate; only `Succeeded`, `Failed`,
  `Deferred`, and `Canceled` end an operation, and
  `Canceled` comes from the service (`canceledBy: "service"`) or the user (`"user"`).

## Real Windows Validation Loop

Automated checks do not cover the visible UI. For service/UI changes, validate on
a Windows VM against the real SYSTEM service.

**If `AGENTS.local.md` exists at the repository root, read it before any VM
validation.** It is gitignored and describes this machine's test rig: which VM
to use and the exact commands for each step below.

1. Start from a clean VM. `just bootstrap <url the VM can reach>`
   builds the binaries and the fixture assets. Serve them with
   `./build/manual-test-server -root build/manual-test/server-root -addr <addr>`.
2. Copy `build/manual-test/vm/bootstrap-vm.ps1`, `test/manual/run-selfserve-smoke.ps1`
   and `test/manual/launch-wails-ui.ps1` to the VM, then run
   `bootstrap-vm.ps1 -BaseUrl <url> -Manifest selfserve_manifest -Catalogs selfserve_catalog -InstallService -StartService -NoPause`
   as an administrator.
3. Machine-assertable gate: `run-selfserve-smoke.ps1` must exit 0 and print
   `SELF-SERVE SMOKE PASSED`.
4. Visible gate: run `launch-wails-ui.ps1` in the logged-on user's desktop session
   with a standard (non-elevated) token, so `sofcat-ui.exe` runs as the standard
   user. It writes a ready marker under `C:\sofcat-test\`, so create that
   directory first. Screenshot the desktop to judge the Home, progress, terminal,
   Activity and offline-cache states.
5. Clean up afterwards: stop the local test server and anything that exposes it
   to the VM, and remove temporary scheduled tasks.

Zip items (placements, receipts, scope exit) have their own loop on the Tapas
Test fixture brand (`test/manual/fixtures/tapas/`). Start from a clean VM, run
`just bootstrap-tapas <url>`, and serve `build/manual-test/tapas/server-root`.
Copy `build/manual-test/tapas/vm/bootstrap-vm.ps1` and
`test/manual/run-tapas-smoke.ps1` to the VM. As an administrator, run
`bootstrap-vm.ps1 -BaseUrl <url> -Slug tapas-test -ProductName "Tapas Test" -Manifest tapas_manifest -Catalogs tapas -InstallService -StartService -NoPause`,
then `run-tapas-smoke.ps1`. The gate is exit 0 and `TAPAS SMOKE PASSED`.
Then run the authorization check from the host:
`test/manual/run-tapas-authz.sh -v <VM>` drives `test/manual/run-tapas-authz.ps1`
step by step through `rig ps`, as the administrator's SSH session and as a
second SSH session signed in as the rig's standard user (`rig ps --as std`),
and must exit 0 and print `TAPAS AUTHZ PASSED` (the guest log is
`C:\sofcat-test\tapas-authz.log`).
Then copy `test/manual/run-tapas-user-sync.ps1` and run it as the logged-on
standard user (`rig run-it --wait test/manual/run-tapas-user-sync.ps1`; create
`C:\sofcat-test\` first): it runs `tapas-test-useragent.exe` and must
print `TAPAS USER SYNC PASSED` (its log is `C:\sofcat-test\tapas-user-sync.log`).
For the logon path, install the branded installer
(`just brand=test/manual/fixtures/tapas/brand setup`) on a clean VM and write
its config, confirm the HKLM `...\CurrentVersion\Run` value
`tapas-test-user-sync`, wait for the startup run to stage the archive and
store the plan, then reboot (the VM autologs on at boot; a plain sign-out does
not log back in) and check the skill appears without opening the storefront.
`uninstall.exe /S` must remove the value and the service. It leaves
`C:\ProgramData\<Name>` alone, the staged archives in `Data\user-artifacts`
included, as it leaves the config; remove the whole directory by hand for a
clean slate.

Nupkg items (`site/src/content/docs/guides/nupkg-items.md`) have their own
loop, on a clean VM with no Chocolatey. `just bootstrap-nupkg` builds
`build/sofcat.exe` and, with embala 0.10.0 from mise, plain-style packages
`sofcat-nupkg-smoke` 1.0.0 and 2.0.0 (`scripts = "generate"`) and
`sofcat-nupkg-bad` 1.0.0, whose install script calls an unknown command,
into a repository at
`build/manual-test/nupkg/server-root` (`test/manual/build-nupkg-repo.sh`).
Serve it on `127.0.0.1:8080` and tunnel that port into the VM. `rig put`
`build/sofcat.exe` and `test/manual/run-nupkg-smoke.ps1` (both land in
`C:\rig`), then `rig ps test/manual/run-nupkg-smoke.ps1` with no arguments.
It writes its own configs, runs `sofcat.exe -config` as SYSTEM through a
temporary `rig-sofcat-nupkg-smoke` scheduled task for the install, update,
uninstall and bad manifests, and must exit 0 and print `NUPKG SMOKE PASSED`
(its log is `C:\sofcat-test\nupkg-smoke.log`). Its last step, informational
only, logs whether plain `-File` would also have failed the bad script.

To validate the installer instead of the raw executables: `just setup`, `rig
put` `build/sofcat-<version>-x86_64-setup.exe` and
`test/manual/install-setup.ps1` on the VM, then run
`install-setup.ps1 -Setup <path to setup.exe>` under `rig ps` (it runs
`setup.exe /S`, waits for it, since the stub is a GUI executable, and exits
with its code). The service starts with no config. Write
`C:\ProgramData\SofCat\config.yaml` (url, manifest, catalogs),
`Restart-Service sofcat`, then run the two gates above; the service keeps its
state in `C:\ProgramData\SofCat\Data`. `install-setup.ps1 -Uninstall` runs
`"C:\Program Files\SofCat\uninstall.exe" /S` the same way; it must remove
the service, the `Run` value, the shortcut, the Add or remove programs entry
and `C:\Program Files\SofCat`, and leave `C:\ProgramData\SofCat` alone.
Running `setup.exe /S` over an existing install must exit non-zero before
copying anything.

## macOS Validation Loop

The macOS twin of the Tapas loop runs on the `mac1` rig (`macos-test-rig`
skill; see `AGENTS.local.md`). The smoke needs root, and the rig has
passwordless sudo.

1. Sync the working tree to `~/src/sofcat` on the Mac (a tarball of the
   checkout without `.git`, `build` and `node_modules`), and `mise install`
   there once.
2. `just bootstrap-tapas-macos` (default base URL `http://127.0.0.1:8080/`)
   runs `just brand=test/manual/fixtures/tapas/brand build-macos pkg`, lays out
   the fixture repository in `build/manual-test/tapas/server-root` and writes
   `build/manual-test/tapas/mac/config.yaml`.
3. Serve it on the Mac:
   `./build/manual-test-server -root build/manual-test/tapas/server-root -addr 127.0.0.1:8080`.
4. Install and configure:
   `sudo installer -pkg build/tapas-test-<version>.pkg -target /`, then
   `sudo install -m 600 -o root -g wheel build/manual-test/tapas/mac/config.yaml "/Library/Application Support/Tapas Test/config.yaml"`
   and `sudo launchctl kickstart -k system/ca.hrndz.tapas-test`.
5. Gate: `sudo test/manual/run-tapas-smoke.sh` must exit 0 and print
   `TAPAS SMOKE PASSED`. It adds `SOFCAT_TEST_MUNKI_REPORT` to the daemon's
   plist, so the real Munki report is never touched, and removes it again at
   the end; a failed run leaves it there until the pkg is reinstalled. That
   variable is a test hook read from the service's own environment, which
   only root can set (launchd's root-owned plist); nothing else honours it.
6. Visible gate: open `/Applications/Tapas Test.app` as the logged-on user and
   screenshot it (`mac show`, or VNC if the capture shows only wallpaper).
7. Clean up: stop the test server, then
   `sudo "/Library/Application Support/Tapas Test/bin/tapas-test" -serviceremove`
   and remove `/Applications/Tapas Test.app`,
   `/Library/Application Support/Tapas Test` and
   `/Library/Application Support/tapas-test-*`. Run that removal through
   `sudo sh -c '...'`: the login shell over SSH is zsh, which aborts the
   whole command when a glob such as `tapas-test-*` matches nothing, and a
   smoke on the leftovers fails at step 8 with "not a clean Mac?".

## Diagnostics

- For diagnostics policy, behavior, and implementation guidance, follow:
  - `ui/ARCHITECTURE.md`
  - `ui/README.md`

## PR Expectations

- Keep PRs focused and explain user-visible behavior changes clearly.
- Call out risks and platform impact (especially Windows).
- Do not include unrelated formatting-only churn.
- After pushing a new branch, open a PR targeting `main`.
- Keep PR title and summary brief but descriptive, and ensure the summary covers all branch changes relative to `main`.
- Only push/open PR for the `agent/<task-slug>` branch created for the current task.

## Safety Rules

- Avoid destructive git commands unless explicitly requested.
- Never remove or overwrite user-authored changes outside the task scope.
- If you encounter unexpected repo state, stop and ask before proceeding.
