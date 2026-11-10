---
title: Data directory
description: What SofCat keeps under ProgramData or Application Support, and who can read or write it.
---

On Windows, everything SofCat writes after install lives under
`C:\ProgramData\SofCat`, as it does under
`/Library/Application Support/SofCat` on macOS:

- `config.yaml`, which management tooling writes.
- `Data\`, the app data directory (`app_data_path` in `config.yaml`, default
  `C:\ProgramData\SofCat\Data`), which holds SofCat's state.
- Any placement of SofCat's own, such as a marketplace, beside them: the
  stock write root is `${ProgramData}\SofCat`
  ([Zip items](/guides/zip-items/#write-roots)).

Under the default `ProgramData` ACL, standard users can read everything there
and create files and folders in it, and they own what they create. That would
let a user read secrets, plant or rewrite SofCat's state, or drop a
`config.yaml` before an administrator writes one. So SofCat gives each of
these its own ACL.

## What it protects

- `config.yaml` may hold `auth_user`, `auth_pass` and TLS client keys.
- `Data\service-manifest.yaml` is the self-serve state, including
  `selected_by`, who selected each item. A user who can write it skips the
  authorization that the service pipe applies, including the per-user check
  on removals.
- `Data\cache\` holds installers and scripts that run as SYSTEM.
- `Data\conditions\` holds admin fact scripts, which run as SYSTEM. What
  they print feeds conditions and templates, and reaches every logged-on
  user's helper with the user-placement plan (`Data\user-plan.json`), so it
  must hold nothing a standard user may not see
  ([Conditions](/guides/conditions/#admin-provided-facts)).
- `Data\conditions-synced\` holds the fact scripts the repository delivers,
  which run as SYSTEM too. The service owns it: each run fetches what the
  repository's index lists, verifies it and deletes the rest, so a file put
  there by hand does not survive
  ([Conditions](/guides/conditions/#scripts-from-the-repository)).
- `Data\nupkg\` holds the kept copy of each installed nupkg, whose
  before-modify and uninstall scripts run as SYSTEM
  ([Nupkg items](/guides/nupkg-items/#the-kept-copy)).
- `Data\sofcat.log` may name paths and items.

`Data\user-artifacts\` is the one entry users read: the staged archives the
per-user helper places into each profile ([Zip items](/guides/zip-items/#installing)).
On macOS, where `Data` is root-only (0700) and no ACL can open one entry of
it, the archives sit beside it instead, in
`/Library/Application Support/SofCat/user-artifacts/` (root-owned, 0755).
No placement may write into it.

The executables are not here. `sofcat.exe`, `sofcat-ui.exe` and
`sofcat-useragent.exe` install to `C:\Program Files\SofCat`, whose default ACL
already lets Users read and execute them and only Administrators and TrustedInstaller write. SofCat never
writes there. On macOS, `sofcat` and `sofcat-useragent` sit in
`/Library/Application Support/SofCat/bin` (root-owned, 0755), and the
helper's LaunchAgent plist in `/Library/LaunchAgents` (root-owned, 0644).

The per-user helper writes outside both: each user's receipts and its
`useragent.log` (capped at 1 MiB plus one backup) live in
`%LOCALAPPDATA%\SofCat`, or `~/Library/Application Support/SofCat` on macOS. The uninstaller leaves them, as it leaves
`C:\ProgramData\SofCat` and the `sofcat` source in the Application log
([Event log](/reference/event-log/)).

## ACL

- **`C:\ProgramData\SofCat`:** SDDL
  `O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)`. The DACL
  is protected, so it inherits nothing from `ProgramData`. SYSTEM and
  Administrators have full control; Users read and execute, inherited by
  SofCat's own placements, which the harnesses need to read. Only
  administrators create entries there. It is set on this directory only, with
  no walk: `Data` and `config.yaml` below it get their own protected DACLs.
- **`Data`:** SDDL `O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)`. Protected
  again. SYSTEM and Administrators have full control, and every subfolder and
  file inherits it. There is no entry for Users or Authenticated Users.
- **Everything in `Data`** has no explicit entries and only inherits from
  `Data`, as after `icacls /reset /t`. The owner is Administrators.
- **`Data\user-artifacts`** gets one explicit entry on top of what it
  inherits: `(A;OICI;0x1200a9;;;BU)`, Users read and execute, inherited by
  the staged archives.
- **`Data\inventory.json`** keeps its own protected ACL, which is stricter
  (see [osquery inventory](/guides/osquery-inventory/)).
- **`config.yaml`:** SDDL `O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)`, SYSTEM and
  Administrators only, owner Administrators, whatever its creator set.

`icacls` shows:

```text
C:\ProgramData\SofCat\ NT AUTHORITY\SYSTEM:(OI)(CI)(F)
                        BUILTIN\Administrators:(OI)(CI)(F)
                        BUILTIN\Users:(OI)(CI)(RX)

C:\ProgramData\SofCat\config.yaml NT AUTHORITY\SYSTEM:(F)
                                   BUILTIN\Administrators:(F)

C:\ProgramData\SofCat\Data\ NT AUTHORITY\SYSTEM:(OI)(CI)(F)
                             BUILTIN\Administrators:(OI)(CI)(F)

C:\ProgramData\SofCat\Data\service-manifest.yaml NT AUTHORITY\SYSTEM:(I)(F)
                                                  BUILTIN\Administrators:(I)(F)

C:\ProgramData\SofCat\Data\user-artifacts\ BUILTIN\Users:(OI)(CI)(RX)
                                            NT AUTHORITY\SYSTEM:(I)(OI)(CI)(F)
                                            BUILTIN\Administrators:(I)(OI)(CI)(F)
```

## When it is applied

- `sofcat.exe -serviceinstall` creates the directories if they are missing
  and applies the ACLs before it registers the service.
- The service applies them again at every start, so a tree deployed by hand
  or created by an older version is fixed on the next start. Inside `Data` it
  resets each entry's owner and ACL, so a file a user created earlier, even
  one locked to that user, loses that user's access. The parent directory is
  not walked, so a placement there keeps its owner and only inherits.

The `Data` ACL is applied only to a directory named `Data` inside one named
`SofCat`, or to a bare `SofCat` directory, which is where an older config's
`app_data_path` still points; only the former also gets the parent ACL. Such
an older config also makes the data directory coincide with the stock write
root, so every stock-root placement is refused until `app_data_path` moves
to `Data` or goes away. A mistyped `app_data_path` such as `C:\ProgramData`
is left alone. Symbolic
links and junctions are skipped, never followed, and a `config.yaml` that is
not a regular file is refused. If any part fails, the install logs a warning
to stderr and the [event log](/reference/event-log/) (ID 9), and the service logs one to `sofcat.log`. The service still runs, and the
run-time check that keeps self-service from changing admin-managed items
still applies.
