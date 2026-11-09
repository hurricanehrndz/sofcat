---
title: Getting started
description: Install the SofCat agent on Windows or macOS, point it at a repository and restart it.
---

:::caution[Pre-beta]
The config keys, catalog format and service protocol can change between
releases without a migration path.
:::

SofCat has two halves. A repository on any web server holds manifests,
catalogs and the items themselves. An agent on each machine reads its manifest
from that repository, keeps the required items in place, and lets a
self-service app install the optional ones for standard users.

## Install on Windows

1. Download `sofcat-<version>-x86_64-setup.exe` from the
   [releases page](https://github.com/hurricanehrndz/sofcat/releases) and
   install it from an elevated prompt:

   ```powershell
   .\sofcat-<version>-x86_64-setup.exe /S
   ```

   The installer puts `sofcat.exe`, `sofcat-ui.exe` and `sofcat-useragent.exe`,
   the per-user helper, in `C:\Program Files\SofCat`, registers and starts the
   `sofcat` service, adds a Start-menu shortcut and an Add or remove programs
   entry, and registers the helper to run at logon. It ships no config, and it
   refuses to install over an existing service.

2. Write `C:\ProgramData\SofCat\config.yaml` and point it at your repository:

   ```yaml
   url: https://example.com/sofcat/
   manifest: example_manifest
   catalogs:
     - example_catalog
   ```

3. Restart the service:

   ```powershell
   Restart-Service sofcat
   ```

Everything the service writes lives under `C:\ProgramData\SofCat`; see
[Data directory](/reference/data-directory/). To remove SofCat, run
`"C:\Program Files\SofCat\uninstall.exe" /S`. It removes the service and the
program files, closes the storefront if it is open, and leaves
`C:\ProgramData\SofCat` alone. The install and the uninstall record each step
in the Windows Application log under the source `sofcat`; see
[Event log](/reference/event-log/).

Nothing is code signed yet. See [Code signing](/reference/code-signing/).

## Install on macOS

Releases do not ship a macOS package yet, so build one on a Mac from a
checkout (see [Developer notes](/contributing/dev/) for the toolchain):

```sh
just build-macos   # the CLI and build/SofCat.app
just pkg           # build/sofcat-<version>.pkg, unsigned
```

Then install it, write the config and restart the daemon:

```sh
sudo installer -pkg build/sofcat-<version>.pkg -target /
sudo install -m 600 -o root -g wheel config.yaml "/Library/Application Support/SofCat/config.yaml"
sudo launchctl kickstart -k system/ca.hrndz.sofcat
```

The package installs the app in `/Applications`, the CLI in
`/Library/Application Support/SofCat/bin/sofcat` and a launchd daemon. The
daemon keeps its state in `/Library/Application Support/SofCat/Data`, which
only root can read. The config stays root-owned and `0600`, since it can hold
`auth_pass`.

## Publish a repository

The repository is plain YAML and files served over HTTP(S):

- a manifest per machine or group says what it gets: required items, optional
  items for the self-service app, removals and updates;
- catalogs say what exists, with each item's installer, version and checks;
- the items themselves (`.msi`, `.exe`, `.ps1`, `.nupkg`, zips).

`makecatalogs` builds `catalogs/<catalog>.yaml` from the repository's
`packages-info/*.yaml`. It is a standalone binary for Linux, macOS and
Windows, so the repository's CI does not need a Windows runner. Run
`makecatalogs --check <repo_path>` as a pull-request gate.

[Configuration](/configuration/) lists the annotated example files to start
from.
