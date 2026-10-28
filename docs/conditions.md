# Installable conditions

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

A condition that names a fact the machine does not have, fails to evaluate,
or does not return a boolean counts as false. A typo hides an item; it never
installs it everywhere. `makecatalogs` reports a syntax error as a problem,
and `makecatalogs -check` fails on it.

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
| `domain` | string | `corp.example.com`; empty when not joined |
| `arch` | string | `amd64`, `arm64` or `x86`: the machine's, not SofCat's |
| `os_version` | string | `10.0.22631.4460` (major.minor.build.UBR) |
| `os_build` | int | `22631`; Windows 11 is `>= 22000` |
| `os_edition` | string | `Enterprise`, `Professional` |
| `machine_manufacturer` | string | `LENOVO` |
| `machine_model` | string | `20XW0055US` |
| `machine_type` | string | `laptop` for a portable chassis, otherwise `desktop` |
| `serial_number` | string | `PF1ABCDE` |
| `ipv4_address` | []string | `["10.20.1.5"]`, without loopback |
| `engine_version` | string | `v1.4.0`, SofCat's version. It is the same in a rebranded build. |

The manufacturer, model, type and serial come from SMBIOS. If SMBIOS can't be
read, those four are missing and conditions that use them are false. Off
Windows, only `hostname`, `arch`, `ipv4_address` and `engine_version` are
set.

## Admin-provided facts

Put scripts in the `conditions` folder of the app data directory
(`C:\ProgramData\SofCat\conditions` by default). Each run executes every
`.ps1` (with Windows PowerShell) and `.exe` there as SYSTEM, in name order.
Each script prints one JSON object on stdout:

```powershell
# C:\ProgramData\SofCat\conditions\10-groups.ps1
$groups = @()
if ($env:COMPUTERNAME -like 'LAB-*') { $groups += 'lab' }
@{ machine_groups = $groups; site = 'east' } | ConvertTo-Json -Compress
```

Then `"lab" in machine_groups` and `site == "east"` work in conditions.

- Keys from all scripts are merged. When two scripts print the same key, the
  later one wins.
- A built-in fact always wins over a script's key of the same name, so a
  script can't spoof `hostname`.
- A script that exits non-zero, runs longer than 60 seconds, or prints
  anything but a JSON object is logged and contributes nothing. Conditions
  that use its keys are false.
- Files starting with `.`, subfolders, and other file types are skipped.

The folder inherits the app data directory's ACL (SYSTEM and Administrators
only; see [data-directory.md](data-directory.md)), so a standard user can't
add a script that runs as SYSTEM.

The scripts run at the start of each managed run. The self-service app's
list reuses the facts from the last run, so opening the app does not run
them. The first list call after the service starts runs them once.

## Seeing the facts

Every run writes the facts it used to `inventory.json` under `Conditions`
(see [osquery-inventory.md](osquery-inventory.md)). `sofcat -checkonly`
prints the same inventory. Skipped items appear in the log as
`skipping item: not installable on this machine`, with the condition.
