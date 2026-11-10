---
title: Required software
description: List software a catalog item needs and sofcat does not install for it.
---

Any catalog item, a zip item or not, can list software it needs and sofcat
does not install for it:

```yaml
depends_check:
  - display_name: Demo Tool
    sofcat: demo-tool
    munki: DemoTool
    script:
      windows: |
        if (Get-Command demo-tool -ErrorAction SilentlyContinue) { exit 0 }
        exit 1
      darwin: |
        #!/bin/sh
        command -v demo-tool
```

- `display_name` is required. It names the software in the deferral message.
- `sofcat` is a catalog item name, checked on every OS: present when sofcat
  finds that item installed on this machine, by the same status check a
  removal uses (its `check` block, or its receipts for a zip item). Unlike
  `dependencies`, sofcat does not install it; the item waits. A name no
  catalog offers this machine counts as missing.
- `munki` is a Munki item name, checked on macOS only: present when it is
  listed under `InstalledItems` in
  `/Library/Managed Installs/ManagedInstallReport.plist`. A missing,
  unreadable or binary report counts as missing. The macOS smoke points
  sofcat at its own report with the `SOFCAT_TEST_MUNKI_REPORT` environment
  variable in the daemon's plist; it is a test hook, not a setting.
- `script` holds one script per OS, under the keys `windows` and `darwin`.
  Exit 0 means present; any other exit, or a script that cannot be run,
  means missing. On Windows it runs in Windows PowerShell as SYSTEM. On
  macOS sofcat writes it to a temporary file only root can read and runs it
  as root, so it must start with a `#!` line. Like the other catalog
  scripts it has no time limit. Its output is not used: it goes to the debug
  log, and the stderr of a failing script is logged.
- An entry is present when any of its checks that apply on this OS passes.
  They are tried in the order `sofcat`, `munki`, `script`, stopping at the
  first that passes. To require several things, list several entries. An
  entry with no check for this OS is skipped.

Before an install or update does anything (download, placement, scripts),
sofcat checks the entries. If any is missing, it defers the item with code
`missing_software` and the message
`Needs Demo Tool. Install it first, then try again.` (several names are
joined with `, `). Check-only runs report the same deferral. Items that
depend on it are deferred with the same code and message. Nothing is
recorded as failed, and the next run checks again. Removals are never
checked.

`makecatalogs -check` reports an entry with no `display_name`, with none of
`sofcat`, `munki` and `script`, or with a `script` key other than `windows`
or `darwin`, an empty script, or a `darwin` script that does not start with
`#!`.

A zip item still does not run `preinstall_script`, `postinstall_script`,
`preuninstall_script`, `postuninstall_script` or `check.script`. Only the
`depends_check` scripts run for it.
