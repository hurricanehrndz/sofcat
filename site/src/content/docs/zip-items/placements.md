---
title: Placements
description: The placements.yaml table that names where zip item trees go, how the storefront labels them and where a build may write.
---

A placement says where a zip item's tree goes. A build may write only under its write roots.

## placements.yaml

`placements.yaml` sits at the repository root, next to `catalogs/` and
`manifests/`. It names the placements. Unknown fields are rejected.
[examples/placements.yaml](https://github.com/hurricanehrndz/sofcat/blob/main/examples/placements.yaml) shows every field.

```yaml
placements:
  codex-skills:
    paths:
      darwin: /etc/codex/skills
      windows: ${USERPROFILE}\.agents\skills
    scope: {windows: user}
    applies_to: [Codex]
```

| Field | Meaning |
|---|---|
| key | The placement name. Variants and item trees use it. |
| `paths` | The placement root, keyed by `darwin` and `windows`. Every placement is a directory: one tree per item at `<root>/<item_name>`. |
| `scope` | `machine` or `user`, keyed like `paths`. An OS left out is `machine`. In `user` scope the per-user helper places the trees and writes the `files`. |
| `applies_to` | Display names, such as `[Claude Code]`. The storefront shows a line such as "Applies to: Claude Code" for an item (see Storefront labels). Display only: it does not limit where an item is placed. |
| `action_labels` | Optional storefront label overrides for the placement's items. Keys: `install`, `installing`, `installed`, `remove`, `removing`, `update`, `cancel`, `pending_install`, `pending_remove`, `applies_to`. |
| `exclusive` | Optional. When true, sofcat does not write into an existing, non-empty root it did not create. |
| `items_dir` | Optional. Item trees go to `<root>/<items_dir>/<item_name>` instead. A clean relative path, slash-separated, each segment one Windows can hold, whose first segment does not start with `.`. |
| `vars` | Optional. Any mapping, read by the placement's templates as `.Placement.Vars`. |
| `files` | Optional. Files rendered from the placement's receipts, each a `template` or a `template_file` with a relative `path` or per-OS `paths` ([Placement files](/zip-items/placement-files/)). |
| `files_atomic` | Optional, with `files`. When true, no file is written unless every one renders and passes its checks; otherwise each is written on its own. |
| `check_facts` | Top level, beside `placements`: facts for `makecatalogs -check` over the built-in samples, for the admin facts templates read. |

Windows paths may use `${ProgramFiles}` and `${ProgramData}`. A path may use
the user's home, `${USERPROFILE}` on Windows or `${HOME}` on macOS, only
when its OS has `user` scope.

Where order matters, placements are taken in file order.

## Storefront labels

Each label key resolves from the item's own `custom_action_labels` (the same
ten keys), then from the first of the item's placements, in file order,
whose `action_labels` sets it, then from the storefront's built-in label.
Only placements with a variant for the machine's OS count, for labels and for
`applies_to`. An item's applies-to line lists the union of those placements'
`applies_to`, in file order, without repeats, after the `applies_to` label
("Applies to" unless a layer sets it). The line is only text: it names where
the item is meant to be used and never scopes the item, so a label such as
"Works in" may read better. For example:

```yaml
# placements.yaml
placements:
  tapas-marketplace:
    applies_to: [Claude Code]
    action_labels: {install: Make available, remove: Withdraw, applies_to: Works in}
    # paths, items_dir, vars and files left out

# packages-info/tapas-optional-plugin.yaml
custom_action_labels: {install: Enable for me}
```

The storefront shows "Enable for me" from the item, "Withdraw" from the
placement, and the built-in "Cancel", since neither sets `cancel`. The remove
label stays "Remove" unless a layer sets `remove`. The applies-to line reads
"Works in: Claude Code", from the placement.

`pending_install` and `pending_remove` word the status of a request waiting
for the next run ("Will be installed", "Will be removed"), which the card
shows beside its Cancel button. Set them along with `install` and `remove`,
so the status reads in the same words as the button that made the request.

## Write roots

A build writes only under its write roots. A brand sets them in
`write-roots.yaml`, compiled into the binaries by `just brand=<dir> build`:

```yaml
darwin: [/Library/Application Support/Acme]
windows: ['${ProgramData}\Acme']
windows_user: ['${USERPROFILE}\.agents']
darwin_user: ['${HOME}/.agents']
```

Without the file, a build uses `${ProgramData}\<NAME>` on Windows and
`/Library/Application Support/<NAME>` on macOS, the directory holding its
config and `Data`, and has no user roots.

Managed policy can add roots to any build, stock or brand. It cannot remove
the build's. On Windows the policy is the key
`HKLM\SOFTWARE\Policies\<NAME>\WriteRoots`, with the `REG_MULTI_SZ` values
`Machine` and `User`:

```powershell
$key = 'HKLM:\SOFTWARE\Policies\SofCat\WriteRoots'
New-Item -Path $key -Force
New-ItemProperty -Path $key -Name Machine -PropertyType MultiString -Value '${ProgramData}\Acme', 'C:\Tools\Acme'
New-ItemProperty -Path $key -Name User -PropertyType MultiString -Value '${USERPROFILE}\.claude'
```

The single quotes keep PowerShell from expanding `${ProgramData}` itself.

On macOS it is the `WriteRoots` dictionary, with `Machine` and `User` arrays
of strings, in `/Library/Managed Preferences/<IDENTIFIER>.plist`. A
configuration profile delivers it as a payload of type `<IDENTIFIER>`:

```xml
<dict>
  <key>PayloadType</key><string>ca.hrndz.sofcat</string>
  <key>PayloadIdentifier</key><string>com.acme.sofcat.writeroots</string>
  <key>PayloadUUID</key><string>5D0C6A52-0E0B-4C8E-9F43-6B1E0C4A2F10</string>
  <key>PayloadVersion</key><integer>1</integer>
  <key>WriteRoots</key>
  <dict>
    <key>Machine</key>
    <array>
      <string>/Library/Application Support/ClaudeCode</string>
      <string>/etc/codex</string>
    </array>
    <key>User</key>
    <array><string>${HOME}/.claude</string></array>
  </dict>
</dict>
```

sofcat reads only the machine-level file, never the per-user ones under
`/Library/Managed Preferences/<user>`, and ignores it unless root owns it and
neither its group nor others can write it.

Policy entries follow the rules of `write-roots.yaml`: a machine root is an
absolute path (on Windows it may use `${ProgramFiles}` and `${ProgramData}`),
and a user root starts with `${USERPROFILE}` or `${HOME}` or is relative to
the user's home. sofcat drops an entry that breaks them and logs a warning
naming it; a policy it cannot read or parse adds no roots. The service and the
per-user helper read the policy once when they start, and log the roots it
adds, so restart the service after changing it.

Writing either location takes an administrator or root. sofcat cannot tell a
policy MDM or Group Policy delivered from one a local administrator wrote, and
trusts both. `config.yaml` and the repository cannot add roots.

sofcat resolves symlinks in a placement path, then refuses it
(`placement_refused`) when it is outside every root for its scope, inside
sofcat's data directory, or inside, equal to or around a directory holding
sofcat's binaries, so the stock root never overlaps `Data` or
`${ProgramFiles}\<NAME>`. An `exclusive` placement that exists, is not empty
and lacks the marker file `.<slug>-owned` is refused too; a root sofcat
creates gets the marker. A repository without `placements.yaml` fails every
zip item with `placement_refused`.
