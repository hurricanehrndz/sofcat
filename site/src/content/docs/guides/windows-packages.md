---
title: Windows packages with embala
description: Build setup.exe, MSI and nupkg installers with embala and deliver them as catalog items.
---

sofcat installs what the repository holds. It does not build installers. For
software that has no vendor installer, such as an in-house tool, a script or
a set of config files, build one with
[embala](https://github.com/hurricanehrndz/embala). embala takes files you
already have and one `embala.toml`, and writes a `setup.exe`, an `.msi` or a
`.nupkg` from Linux, macOS or Windows, with no WiX and no Windows build
host. sofcat's own `setup.exe` is built this way.

The [embala wiki](https://github.com/hurricanehrndz/embala/wiki) documents
every format and config field. This page covers what sofcat needs from each
format and how to write the catalog item for it.

| embala format | Catalog `type` | Fits | Status check |
| --- | --- | --- | --- |
| `.msi` | `msi` | Files under Program Files, a Windows service, a PATH entry | `check.registry` |
| `setup.exe` | `exe` | Install-time logic in Lua, such as registry writes or running commands | `check.registry` or `check.file` |
| `.nupkg` | `nupkg` | Command-line tools on PATH, or your own PowerShell install scripts | The kept copy, no `check` block needed |

Start with the MSI. Windows Installer does the upgrade and the removal, and
the catalog item needs no arguments. Use `setup.exe` when the install needs
logic the MSI cannot express, and a nupkg when PowerShell scripts are how you
already install the thing.

## One config, three formats

All three formats read the same `[package]` table. embala builds each format
that has a section:

```toml
[package]
name = "hello"
display-name = "Hello"
version = "1.2.0"
identifier = "com.example.hello"
publisher = "Example Inc."
description = "A tiny CLI."

[msi]
arch = "x86_64"
main-executable = "hello.exe"
files = [{ src = "hello.exe", dest = "hello.exe" }]

[setup]
arch = "x86_64"
install-mode = "per-machine"
main-executable = "hello.exe"
files = [{ src = "hello.exe", dest = "hello.exe" }]

[nupkg]
style = "plain"
scripts = "generate"
files = [{ src = "hello.exe", dest = "hello.exe" }]
```

```shell
$ embala build
msi: wrote dist/hello-1.2.0-x86_64.msi
nupkg: wrote dist/hello-1.2.0.nupkg
setup: wrote dist/hello-1.2.0-x86_64-setup.exe
```

`embala build --formats msi` builds one of them.
[Getting Started](https://github.com/hurricanehrndz/embala/wiki/Getting-Started)
covers installing embala and the build command, and the
[Configuration Reference](https://github.com/hurricanehrndz/embala/wiki/Configuration-Reference)
lists every field.

Ship one format per product. The MSI and the nupkg install to different
directories, and nothing stops both from landing on one machine.

## MSI items

embala's MSI is small on purpose. It installs the files under
`C:\Program Files\<display-name>` for all users, adds one Start-menu shortcut
and an Add or remove programs entry, and can register one Windows service and
append its directory to the system PATH. It shows no wizard. The
[MSI page](https://github.com/hurricanehrndz/embala/wiki/MSI) has the
details.

```yaml
hello:
  display_name: Hello
  version: 1.2.0
  check:
    registry:
      name: Hello
      version: 1.2.0
  installer:
    type: msi
    location: packages/apps/hello/hello-1.2.0-x86_64.msi
    hash: 3f1c9a7e52b84d06a1e9c3b7d25f8a64e0b1c7d93a5f2e86b4d0c1a9e7f35b28
  uninstaller:
    type: msi
    location: packages/apps/hello/hello-1.2.0-x86_64.msi
    hash: 3f1c9a7e52b84d06a1e9c3b7d25f8a64e0b1c7d93a5f2e86b4d0c1a9e7f35b28
```

- sofcat runs `msiexec /i <file> /qn /norestart`, followed by any
  `installer.arguments`, and removes the item with
  `msiexec /x <file> /qn /norestart`.
- Exit codes 1641 and 3010, which mean a restart is needed, count as success.
  A `success_codes` list replaces that default.
- `check.registry.name` is the `display-name` from `embala.toml`, which is
  the name Add or remove programs shows. sofcat compares it ignoring case and
  reads the machine-wide entries only. The item counts as installed when that
  entry's version is at least `check.registry.version`.

Keep `identifier` the same across releases. embala derives the MSI's upgrade
code from it, so a newer MSI replaces the older install and leaves one Add or
remove programs entry. An older MSI refuses to install over a newer one, so
lowering the catalog version does not roll a machine back.

## Names that carry a version

Some vendor installers put the version in the Add or remove programs name,
such as `7-Zip 26.06 (x64 edition)` or `WinSCP 6.7.1`. A `check.registry.name`
then matches one release only. Once the catalog moves to the next release, the
older one installed on a machine reads as absent, so an update never runs and
a removal does nothing.

Use `check.registry.name_regex` in place of `name` for these:

```yaml
check:
  registry:
    name_regex: '7-Zip [\d.]+ \(x64 edition\)'
    version: 26.07
```

- It needs sofcat 5.9.0 or later. An older agent ignores the key, finds
  nothing installed and installs the item again on every run.
- The pattern must match the whole name, not part of it. `7-Zip` alone does
  not match `7-Zip 26.06 (x64 edition)`.
- It ignores case, as `name` does. sofcat trims the spaces around the
  installed name, but uses the pattern as written, so `' 7-Zip.*'` never
  matches.
- It uses Go's [RE2 syntax](https://github.com/google/re2/wiki/Syntax).
- Put it in YAML single quotes, so the backslashes stay as written.
- Set `name` or `name_regex`, not both. `makecatalogs -check` reports an item
  with both, or with a pattern that does not compile. An agent that gets such
  an item records its check as failed and neither installs nor removes it.

Match as tightly as the name allows. A loose pattern can match several
entries, such as a product and its helper. Any matching entry makes the item
count as installed, so a removal runs when any one matches. The item is up to
date only when a matching entry is at `version` or above.

An `msi` uninstaller runs `msiexec /x` on the catalog's own MSI. When the
pattern matches an older release with another product code, msiexec does not
know that MSI as installed and exits with 1605, so the removal fails on every
run. To remove older releases too, use a `ps1` uninstaller that finds the
installed entry and runs its uninstall command. Listing 1605 in
`success_codes` only hides the failure: the old release stays.

## Exe items

embala's `setup.exe` is a wizard installer that also runs unattended. sofcat
runs the file with the item's `arguments` and nothing else, so the item must
pass `/S`. Without it the wizard opens in the SYSTEM session, where nobody
can click it, and the install never finishes.

```yaml
hello:
  display_name: Hello
  version: 1.2.0
  check:
    registry:
      name: Hello
      version: 1.2.0
  installer:
    type: exe
    location: packages/apps/hello/hello-1.2.0-x86_64-setup.exe
    hash: 65bf42b15a05b13197e4dd6cdf181e39f30d47feb2cb6cc929db21cd634cd36f
    arguments:
      - /S
      - /mode=per-machine
  uninstaller:
    type: ps1
    location: packages/apps/hello/uninstall-hello.ps1
    hash: 676dcb69da99728feb8af3231e863dbb9639dc09f409749a74dd5c08dc2fb809
```

- Install per machine. Set `install-mode = "per-machine"` in `[setup]`, or
  pass `/mode=per-machine`. embala's default is per user, and sofcat installs
  as SYSTEM, so a per-user install lands in the SYSTEM profile where no user
  finds it and `check.registry` does not look.
- A per-machine install goes to `C:\Program Files\<name>`, the package
  `name`, not the `display-name` the MSI uses. `/D=<dir>` picks another
  directory.
- `exe` items have no default success codes. Only exit code 0 succeeds unless
  the item lists `success_codes`.
- With the declarative `[setup]` fields, the installer writes an Add or
  remove programs entry named after `display-name`, so `check.registry` works
  as for the MSI. A raw Lua `script` writes that entry only if the script
  does. Check a file with `check.file` otherwise.

To remove the item, run the `uninstall.exe` the installer left in its install
directory. sofcat's `exe` uninstaller runs a file from the repository, and
that file is not in the repository, so use a small `ps1` uninstaller that
calls it:

```powershell
$uninstaller = Join-Path $env:ProgramFiles 'hello\uninstall.exe'
if (-not (Test-Path -LiteralPath $uninstaller)) { exit 0 }
$process = Start-Process -FilePath $uninstaller -ArgumentList '/S' -Wait -PassThru
exit $process.ExitCode
```

`Start-Process -Wait` matters. `uninstall.exe` is a GUI executable, and
PowerShell does not wait for one that it calls directly.

The [Setup-Exe page](https://github.com/hurricanehrndz/embala/wiki/Setup-Exe)
lists the flags, the install modes and what the uninstaller removes.
[Lua Scripting](https://github.com/hurricanehrndz/embala/wiki/Lua-Scripting)
covers install-time logic, and
[Porting from NSIS](https://github.com/hurricanehrndz/embala/wiki/Porting-from-NSIS)
maps an existing NSIS installer onto it.

## Nupkg items

A nupkg item installs an embala-built `.nupkg` by running the package's own
PowerShell scripts as SYSTEM. sofcat never runs `choco.exe`, and the machine
needs no Chocolatey.

```toml
[nupkg]
style = "plain"
scripts = "generate"
files = [{ src = "hello.exe", dest = "hello.exe" }]
```

- `scripts` is required, and it needs embala 0.10.0 or later. A package
  without it carries no install script, and sofcat fails it.
- `scripts = "generate"` writes an install and an uninstall script. The
  install copies the files to `C:\Program Files\<display-name> (nupkg)` and
  appends that directory to the machine PATH. A table,
  `scripts = { install = "...", uninstall = "..." }`, packs your own scripts.
- `style = "plain"` builds a package that `choco install` refuses, which
  suits a package meant only for sofcat. `style = "embedded"` with `scripts`
  works under both.
- `style = "download"` does not work. Its script calls Chocolatey commands,
  and sofcat provides none.

The catalog item needs a `hash` and, with no `check` block, a `package_id`
equal to the `name` in `embala.toml`:

```yaml
hello:
  display_name: Hello
  version: 1.2.0
  installer:
    type: nupkg
    location: packages/apps/hello/hello-1.2.0.nupkg
    hash: 3f1c9a7e52b84d06a1e9c3b7d25f8a64e0b1c7d93a5f2e86b4d0c1a9e7f35b28
    package_id: hello
  uninstaller:
    type: nupkg
    location: packages/apps/hello/hello-1.2.0.nupkg
    hash: 3f1c9a7e52b84d06a1e9c3b7d25f8a64e0b1c7d93a5f2e86b4d0c1a9e7f35b28
```

sofcat keeps a copy of each package it installs and reads the item's status
from that copy. [Nupkg items](/guides/nupkg-items/) covers the kept copy, the
order the scripts run in and the move from Chocolatey. On the embala side,
the `scripts` and `plain` keys are in the README under
[nupkg install scripts and shims](https://github.com/hurricanehrndz/embala#nupkg-install-scripts-and-shims).
The wiki's [Nupkg page](https://github.com/hurricanehrndz/embala/wiki/Nupkg)
describes the package as Chocolatey installs it.

## Signing

embala signs the `.msi` and the `setup.exe` when `embala.toml` has a `[sign]`
section, and never a `.nupkg`. See
[Code Signing](https://github.com/hurricanehrndz/embala/wiki/Code-Signing).
sofcat does not check signatures. It checks each download against the
catalog's `hash`.

## Zip-only builds

A [zip-only build](/zip-items/building/#zip-only-builds) has no command
installers and rejects `msi`, `exe`, `ps1` and `nupkg` items.
