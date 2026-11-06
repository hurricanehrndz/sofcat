---
title: Nupkg items
description: Install embala-built nupkgs by running the package's own PowerShell scripts, without Chocolatey.
---

A nupkg item installs a `.nupkg` built by
[embala](https://github.com/hurricanehrndz/embala) 0.10.0 or later. sofcat
opens the package itself, runs the package's own PowerShell scripts as SYSTEM
and keeps a copy of the package. It never runs `choco.exe` and reads nothing
under `%ProgramData%\chocolatey`, so the machine needs no Chocolatey. Nupkg
items are Windows only.

```yaml
hello:
  display_name: Hello
  version: 1.2.0
  installer:
    type: nupkg
    location: packages/apps/hello/hello.1.2.0.nupkg
    hash: 3f1c9a7e52b84d06a1e9c3b7d25f8a64e0b1c7d93a5f2e86b4d0c1a9e7f35b28
    package_id: hello
  uninstaller:
    type: nupkg
    location: packages/apps/hello/hello.1.2.0.nupkg
    hash: 3f1c9a7e52b84d06a1e9c3b7d25f8a64e0b1c7d93a5f2e86b4d0c1a9e7f35b28
```

- `hash` is the package's sha256. sofcat refuses a package without one.
- `package_id` is the id in the package's `.nuspec`. If it is set and the
  nuspec says otherwise (compared ignoring case), the install fails and names
  both ids. An item with no `check` block needs it (see [Status](#status)).
- `version` must equal the nuspec version, compared as versions, so `1.2`
  equals `1.2.0`. A mismatch fails the install and names both versions. An
  item with no `version` takes the nuspec version.
- `uninstaller` is needed to remove the item. sofcat downloads it for every
  removal but runs its uninstall script only when there is no kept copy.
- `success_codes` works as for `msi` items: 1641 and 3010 count as success by
  default, and a listed set replaces that default.

## Building the package

Build the package with embala 0.10.0 or later and set its `scripts` key,
either `scripts = "generate"` or your own scripts. embala's `plain` style
builds a package that `choco install` refuses, which is fine for sofcat.

A package built before embala 0.10.0, or without `scripts`, carries no
install script; Chocolatey's shims were what put its exe on PATH. sofcat
fails it with `<display name>: package has no install script`. Rebuild it.

sofcat runs only the package's scripts and provides no Chocolatey helpers.
A community package, or any script that calls `Install-ChocolateyPackage`,
`Get-ChocolateyWebFile` or another Chocolatey command, fails. That includes
embala's `download` style. There is no fallback to `choco.exe`.

Install either the embala `.msi` or the generated-script `.nupkg` of a
product on a machine, never both. embala installs them to different
directories, and with the MSI's `add-to-path` both end up on PATH. The first
one wins, so uninstalling it hands the command to the other copy.

## Scripts

sofcat looks for two sets of script names under the package's `tools`
directory, matching names regardless of case:

| Script | Plain set (embala `plain` style) | Chocolatey set |
| --- | --- | --- |
| Install | `install.ps1` | `chocolateyInstall.ps1` |
| Uninstall | `uninstall.ps1` | `chocolateyUninstall.ps1` |
| Before-modify | `before-modify.ps1` | `chocolateyBeforeModify.ps1` |

For each script, the plain name wins when the package has both. embala's
plain style also ships a `chocolateyInstall.ps1` that only throws, to stop
`choco install`, so sofcat must not pick it. Before-modify is optional;
`scripts = "generate"` writes none.

sofcat runs each script with Windows PowerShell 5.1, from the package's
`tools` directory, through this command:

```powershell
$ErrorActionPreference = 'Stop'; & '<script>'; if ($LASTEXITCODE) { exit $LASTEXITCODE }; exit 0
```

So, unlike a `ps1` item, which runs its script with plain `-File`:

- A call to an unknown command, or any non-terminating error such as
  `Write-Error`, stops the script and fails the action. Under `-File` both
  would exit 0. Chocolatey runs its scripts with `Stop` too, so scripts
  written for it expect this.
- The exit code is the script's `exit` value, or else the last native
  command's. A script that runs a native command, accepts its non-zero exit
  and carries on still fails the action with that code, unless it ends with
  `exit 0`.

sofcat sets none of Chocolatey's environment variables, such as
`$env:ChocolateyPackageName`.

The scripts run in this order:

- **Install, no kept copy.** The new package's install script.
- **Install or update over a kept copy.** The kept copy's before-modify, then
  the new package's install script. This holds for an upgrade and for a
  reinstall of the same version.
- **Uninstall, with a kept copy.** The kept copy's before-modify, then its
  uninstall script, then sofcat removes the kept copy. A package with no
  uninstall script fails the uninstall, and the kept copy stays.
- **Uninstall, no kept copy.** sofcat extracts the `uninstaller` file and
  runs its uninstall script, with no before-modify.

A before-modify that fails logs a warning, and the action goes on, as under
Chocolatey.

## The kept copy

After the install script succeeds, sofcat keeps the whole extracted package
at `<app_data_path>\nupkg\<id>`, where `<id>` is the nuspec id in lower case.
With the default `app_data_path` that is `C:\ProgramData\SofCat\Data\nupkg\hello`.
It sits under the `Data` ACL, SYSTEM and Administrators only
([data-directory.md](/reference/data-directory/)). There is one copy per id, not per
item or version: two items that ship the same id share it.

Before anything extracts, sofcat checks the hash and refuses a package with
an absolute path, a `..` segment, a non-regular file, a Windows reserved
name, two names that differ only in case, more than 10,000 entries, a file
over 256 MB, more than 1 GB in total or a compression ratio over 100:1. It decodes `%XX` in entry names
first, so `%2e%2e/` is refused like `../`.

An install extracts into `nupkg\.staging-<id>`, and the scripts run from
there. When the install script succeeds, the old copy moves to
`nupkg\.old-<id>`, staging takes its place and sofcat deletes the old copy.
When it fails, sofcat removes staging and leaves the old copy as it was. A
staging directory left by an interrupted run is deleted at the next install
of that id.

sofcat retries the move out of staging for about 2 seconds. On Windows, a
process that the install script leaves running from the `tools` directory,
or with it as its working directory, holds staging open, and if it outlives
that retry the install fails. Copy the payload elsewhere before starting it,
and pass `Start-Process` a `-WorkingDirectory` outside the package.

## Status

A nupkg item with a `check` block is checked by that block, as for any other
type.

A nupkg item with no `check` block and with `installer.package_id` is checked
against the kept copy for that id:

- `install` acts unless a kept copy exists whose version is at least the
  item's `version`. An item with no `version` accepts any kept copy.
- `update` acts only when a kept copy exists and its version is lower.
- `uninstall` acts when a kept copy exists.

A kept copy whose nuspec sofcat cannot read counts as installed at no
version, so `install` and `update` both reinstall over it.

A nupkg item with neither a `check` block nor `installer.package_id` has
nothing to check against. sofcat logs `Not enough data to check the current
status` and takes no action.

The kept copy records what sofcat installed, not what is on disk now. If
someone deletes the install directory by hand, such as
`C:\Program Files\Hello (nupkg)`, the item still reads as installed until
its version changes. Add a `check` block if that matters. Deleting
`Data\nupkg\<id>` makes the next run install the item again, with no
before-modify.

## makecatalogs -check

`makecatalogs` reports:

- a nupkg installer without `hash`
- a nupkg uninstaller with a `location` but no `hash`
- a nupkg item with no `check` block and no `installer.package_id`

A normal run warns about these; `makecatalogs -check` fails on them.

## Moving from Chocolatey

sofcat does not migrate items Chocolatey installed, and it never changes
Chocolatey's state. What happens to such an item depends on its status:

- With a `check` block that still passes, sofcat leaves the item alone until
  its version changes. It then installs the new version with no kept copy, so
  no before-modify runs.
- With no `check` block, there is no kept copy yet, so the next run installs
  the item over the Chocolatey install.

Either way, `%ProgramData%\chocolatey\lib\<id>` and Chocolatey's shims stay
behind. That stale `lib` directory still satisfies `requires_software` with
`chocolatey: <id>` on other items.

For a clean switch, remove the Chocolatey install first with a
`preinstall_script`. It runs before each install or update sofcat makes of
the item and does nothing once Chocolatey's copy is gone:

```yaml
hello:
  preinstall_script: |
    $choco = Join-Path $env:ProgramData 'chocolatey\bin\choco.exe'
    $lib = Join-Path $env:ProgramData 'chocolatey\lib\hello'
    if ((Test-Path $choco) -and (Test-Path $lib)) {
      & $choco uninstall hello -y
      if ($LASTEXITCODE -notin 0, 1641, 3010) { exit $LASTEXITCODE }
    }
    exit 0
```

Any other exit code fails the install, and the next run tries again. 1641
and 3010 mean Chocolatey's uninstall wants a restart, and the install goes on.

## Required software

`requires_software` with `chocolatey: <id>` still checks for
`%ProgramData%\chocolatey\lib\<id>` ([zip-items.md](/guides/zip-items/#required-software)).
It names software sofcat never installs, so a package sofcat installed does
not satisfy it. For an item that needs a nupkg item, list the nupkg item
under `dependencies`, and sofcat installs it first.

## Zip-only builds

A [zip-only build](/guides/zip-items/#zip-only-builds) has no nupkg engine and
rejects nupkg items.
