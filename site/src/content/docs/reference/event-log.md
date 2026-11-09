---
title: Event log
description: Where SofCat's install, uninstall, start and stop steps are recorded on Windows, and the event IDs they use.
---

The running service logs to `C:\ProgramData\SofCat\Data\sofcat.log`. The
commands that install and remove it do not: on Windows, every step and the
outcome of `sofcat.exe -serviceinstall`, `-serviceremove`, `-servicestart`
and `-servicestop` go to the Windows Application log. The installer runs the
first two, so `setup.exe` and `uninstall.exe` leave their record there, even
for a silent (`/S`) run whose output nobody sees. Each entry also goes to
stderr, as before.

To read them, open Event Viewer, then **Windows Logs → Application**, and
filter by the source, which is the brand's slug (`sofcat` for SofCat). From
PowerShell:

```powershell
Get-WinEvent -FilterHashtable @{LogName = 'Application'; ProviderName = 'sofcat'} -MaxEvents 50
```

The entry's level is the record's: Information, Warning or Error. Its text is
the message followed by the details as `key=value` pairs, for example
`ended a running agent pid=4120 image="C:\Program Files\SofCat\sofcat-useragent.exe"`.

## Event IDs

| ID | Step |
| -- | ---- |
| 1 | Anything without its own ID below |
| 2 | A command started, succeeded or failed; the failure carries the error |
| 3 | The service registered with the service manager, or that registration undone after a failed install |
| 4 | The service started, or was already running |
| 5 | The service stopped, or was already stopped |
| 6 | The service deleted, or found not installed by `-serviceremove` |
| 7 | The logon `Run` value `<slug>-user-sync` written |
| 8 | The `Run` value removed, or undone after a failed install |
| 9 | The ACL on the data directory or `config.yaml` applied, or a warning that it could not be |
| 10 | `-serviceremove` ended a running per-user helper or storefront |
| 11 | An ended process had not exited 5 seconds later; its executable may be left behind |
| 12 | `-serviceremove` could not do part of its sweep (privilege, process list, open or end a process) |

The IDs never change meaning; new kinds of step get new IDs.

## The source

`-serviceinstall` registers the source in the Application log before its
other steps, as an EventCreate source (the messages file Windows ships for
`eventcreate.exe`, which accepts IDs 1 to 1000). A source that already
exists, from a reinstall or an upgrade, is left as it is. `-serviceremove`,
`-servicestart` and `-servicestop` register it too when it is missing, for a
machine installed by an older version.

Nothing removes the source: it stays registered after uninstall, as
`C:\ProgramData\SofCat` stays, so the uninstall's own entries remain readable.
Remove it by hand for a clean slate:

```powershell
Remove-Item 'HKLM:\SYSTEM\CurrentControlSet\Services\EventLog\Application\sofcat'
```

If the source cannot be registered or opened, the command says so on stderr
and logs to stderr only. Logging never fails or blocks an install or a
remove.

On macOS the same commands log to stderr only.
