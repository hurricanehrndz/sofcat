---
title: Installable conditions
description: Limit a catalog item to the machines whose facts match an installable_condition expression.
---

A catalog item can say which machines it may be installed on with
`installable_condition`, an expression over facts about the machine. This is
Munki's `installable_condition`, with a different expression language.

```yaml
item_name: ChromeArm64
catalog: production
installable_condition: arch == "arm64" && os_build >= 22000
installer:
  type: msi
  location: packages/chrome/GoogleChromeArm64.msi
```

Use it for what a package needs, such as an architecture, a Windows build or
a hardware model. To decide which machines should get an item, use
manifests.

## What happens when the condition is false

| Path | Effect |
|---|---|
| `managed_installs`, self-service installs, `update_for` updaters | Skipped and logged. The inventory lists it under `Warnings`, not as a failure. |
| `optional_installs` | Not offered. The self-service app does not list it, and an install request for it is refused. |
| `managed_updates` | Skipped. |
| A dependency | The item that depends on it fails. |
| `managed_uninstalls` and self-service removal | Not affected. An item stays removable after its condition stops holding. |
| Already installed | Left installed. A condition never removes anything. |

A condition has three results: true, false or unknown. It is unknown when
it names a fact the machine does not have, including a misspelled fact. A
condition that fails to evaluate or does not return a boolean is false. For
`installable_condition`, unknown counts as false, and the log names the
missing facts. A typo hides an item; it never installs it everywhere.
`makecatalogs` reports a syntax error as a problem, and
`makecatalogs -check` fails on it.

### Variants

Each catalog holds one item per name. For one name with per-architecture
variants, put each variant in its own catalog and list both catalogs in the
config or manifest. The first catalog whose item passes its condition wins:

```yaml
# catalogs/arm64.yaml
Chrome:
  installable_condition: arch == "arm64"
  installer: {type: msi, location: packages/chrome/GoogleChromeArm64.msi}
# catalogs/production.yaml
Chrome:
  installer: {type: msi, location: packages/chrome/GoogleChrome.msi}
```

## Conditional items in manifests

A manifest can list items for some machines only with `conditional_items`, as
in Munki. Each block has a `condition` and any of `managed_installs`,
`optional_installs`, `default_installs`, `managed_uninstalls`,
`managed_updates` and nested `conditional_items`:

```yaml
name: site
managed_installs: [GoogleChrome]
conditional_items:
  - condition: os == "windows"
    managed_installs: [7zip]
    conditional_items:
      - condition: arch == "arm64"
        managed_installs: [ArmTools]
  - condition: '"lab" in machine_groups'
    optional_installs: [CUDA]
```

- A true block adds its lists to the manifest's. Its nested blocks are then
  evaluated the same way.
- A false block adds nothing, and neither do its nested blocks.
- An unknown block holds its items, nested blocks included. SofCat neither
  installs nor removes them, and does not offer them for self-service. The
  run log warns about each held condition and the run report records it with
  the reason "facts unavailable". An admin fact script that fails holds the
  blocks that use its keys instead of removing their items.

The self-service list and the managed run read the same expanded manifests.
`makecatalogs -check` checks the syntax of every `conditional_items`
condition in the repository's `manifests` folder.

## Expressions

Conditions use [expr](https://expr-lang.org/docs/language-definition).

| Need | Expression |
|---|---|
| Compare | `arch == "arm64"`, `os_build >= 22000`, `os_edition != "Home"` |
| Combine | `&&`, `\|\|`, `!`, parentheses |
| Match text | `hostname startsWith "LAB-"`, `domain endsWith ".example.com"`, `machine_model contains "ThinkPad"`, `hostname matches "^LAB-[0-9]+$"` |
| One of | `machine_model in ["20XW0055US", "20XX0011US"]` |
| List contains | `"lab" in machine_groups` |
| Any element | `any(ipv4_address, # startsWith "10.20.")` |
| Versions | `version(os_version) >= "10.0.22631.4460"`, `version(engine_version) >= "1.4"` |
| Dates | `now() > date("2026-12-01")` |

Wrap a version in `version(...)`, or `"10.0.9"` compares greater than
`"10.0.22631"` as text. `version()` drops a leading `v` and anything from the
first `-` or `+`, so a build such as `v1.4.0-3-gabc123` compares as `1.4.0`.
A value that is not a version, such as `dev`, makes the condition false.

In Munki's predicate syntax, `os_vers BEGINSWITH "10"` becomes
`os_version startsWith "10"`, `ANY ipv4_address CONTAINS "10."` becomes
`any(ipv4_address, # contains "10.")`, and `AND`/`OR`/`NOT` become
`&&`/`||`/`!`.

## Built-in facts

| Fact | Type | Example |
|---|---|---|
| `hostname` | string | `LAB-GPU-01` |
| `domain` | string | `corp.example.com`; empty when not joined. Windows only. |
| `os` | string | `windows`, `darwin` or `linux` |
| `arch` | string | `amd64`, `arm64` or `x86`: the machine's, not SofCat's |
| `os_version` | string | `10.0.22631.4460` (Windows: major.minor.build.UBR), `15.1` (macOS) |
| `os_build` | int | `22631`; Windows 11 is `>= 22000`. Windows only. |
| `os_edition` | string | `Enterprise`, `Professional`. Windows only. |
| `machine_manufacturer` | string | `LENOVO`; `Apple` on macOS |
| `machine_model` | string | `20XW0055US`, `Mac16,12` |
| `machine_type` | string | `laptop` for a portable chassis (on macOS a MacBook model or a Mac with an internal battery), otherwise `desktop` |
| `serial_number` | string | `PF1ABCDE` |
| `ipv4_address` | []string | `["10.20.1.5"]`, without loopback |
| `engine_version` | string | `v1.4.0`, SofCat's version. It is the same in a rebranded build. |

On Windows the manufacturer, model, type and serial come from SMBIOS. If SMBIOS
can't be read, those four are missing and conditions that use them are
unknown.

On macOS `os_version` comes from `sw_vers -productVersion`, `machine_model`
from `sysctl -n hw.model`, `serial_number` from `ioreg -rd1 -c
IOPlatformExpertDevice`, and `machine_type` from the model or, on Apple
silicon, whose models do not say `MacBook`, from `ioreg -rd1 -c
AppleSmartBattery`. Each command gets the same 60 seconds as an admin script.
A command that fails leaves its facts missing, so conditions that use them are
unknown. macOS sets no `os_build`, `os_edition` or `domain`: its build string
is not a Windows build number.

On Linux, where SofCat runs only for development, only `hostname`, `os`,
`arch`, `ipv4_address` and `engine_version` are set.

## Admin-provided facts

Put scripts in the `conditions` folder of the app data directory
(`C:\ProgramData\SofCat\Data\conditions` or
`/Library/Application Support/SofCat/Data/conditions` by default). Each run
executes them in name order and reads one JSON object from each one's stdout:

- On Windows, every `.ps1` (with Windows PowerShell) and `.exe`, as SYSTEM.
- On macOS, every file that is owned by root, not group- or world-writable,
  and executable, run directly as root, so it needs a `#!` line. Any other
  file, a symbolic link included, is skipped and logged.

For example:

```powershell
# C:\ProgramData\SofCat\Data\conditions\10-groups.ps1
$groups = @()
if ($env:COMPUTERNAME -like 'LAB-*') { $groups += 'lab' }
@{ machine_groups = $groups; site = 'east' } | ConvertTo-Json -Compress
```

Then `"lab" in machine_groups` and `site == "east"` work in conditions.

Templates read the same facts: a placement file template or a tree template
([Zip items](/zip-items/tree-templates/)) calls `fact "site"` or
`cond "site == \"east\""`, so a value management tooling wants in a rendered
file comes from a script here, not from a new setting. The service also
hands the whole facts map to every logged-on user's helper with the
user-placement plan, so user-scope trees render with the same values and
the helper runs no script. A script must therefore not print anything a
standard user may not see, and a value a machine-scope template writes lands
in a machine-wide directory every user can read.

- Keys from all scripts are merged. When two scripts print the same key, the
  later one wins.
- A built-in fact always wins over a script's key of the same name, so a
  script can't spoof `hostname`.
- A script that exits non-zero, runs longer than 60 seconds, or prints
  anything but a JSON object is logged and contributes nothing. Conditions
  that use its keys are unknown.
- Files starting with `.`, subfolders, and other file types are skipped.

The folder inherits the app data directory's ACL (SYSTEM and Administrators
only; see [Data directory](/reference/data-directory/)), so a standard user can't
add a script that runs as SYSTEM. On macOS the Data directory is root-owned
with mode 0700, and the ownership and mode rule above holds for each script
as well.

The scripts run at the start of each managed run. The self-service app's
list reuses the facts from the last run, so opening the app does not run
them. The first list call after the service starts runs them once.

## Seeing the facts

Every run writes the facts it used to `inventory.json` under `Conditions`
(see [osquery inventory](/guides/osquery-inventory/)). `sofcat -checkonly`
prints the same inventory. Skipped items appear in the log as
`skipping item: not installable on this machine`, with the condition.
