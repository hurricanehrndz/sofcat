# Data directory ACL

SofCat keeps its state in the app data directory (`app_data_path` in
`config.yaml`, default `C:\ProgramData\sofcat`). Under the default
`ProgramData` ACL, standard users can read everything there and create files
and folders in it, and they own what they create. That would let a user read
secrets and plant or rewrite SofCat's state. So SofCat gives the directory
its own ACL.

## What it protects

- `config.yaml` may hold `auth_user`, `auth_pass` and TLS client keys.
- `service-manifest.yaml` is the self-serve state. A user who can write it
  skips the authorization that the service pipe applies.
- `cache\` holds installers and scripts that run as SYSTEM.
- `sofcat.log` may name paths and items.

The executables are not here. `sofcat.exe` and `sofcat-ui.exe` install to
`C:\Program Files\SofCat`, whose default ACL already lets Users read and
execute them and only Administrators and TrustedInstaller write.

## ACL

- **Root:** SDDL `O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)`. The DACL is
  protected, so it inherits nothing from `ProgramData`. SYSTEM and
  Administrators have full control, and every subfolder and file inherits it.
  There is no entry for Users or Authenticated Users.
- **Everything else** has no explicit entries and only inherits from the
  root, as after `icacls /reset /t`. The owner is Administrators.
- **`inventory.json`** keeps its own protected ACL, which is stricter (see
  [osquery-inventory.md](osquery-inventory.md)).

`icacls` shows:

```text
C:\ProgramData\SofCat\ NT AUTHORITY\SYSTEM:(OI)(CI)(F)
                        BUILTIN\Administrators:(OI)(CI)(F)

C:\ProgramData\SofCat\service-manifest.yaml NT AUTHORITY\SYSTEM:(I)(F)
                                             BUILTIN\Administrators:(I)(F)
```

## When it is applied

- `sofcat.exe -serviceinstall` creates the directory if it is missing and
  applies the ACL before it registers the service.
- The service applies it again at every start, so a tree deployed by hand or
  created by an older version is fixed on the next start. It resets each
  entry's owner and ACL, so a file a user created earlier, even one locked to
  that user, loses that user's access.

The ACL is applied only to a directory named `sofcat`, so a mistyped
`app_data_path` such as `C:\ProgramData` is left alone. Symbolic links and
junctions inside the tree are skipped, never followed. If any part fails,
the install prints a warning and the service logs one to `sofcat.log`. The
service still runs, and the run-time check that keeps self-service from
changing admin-managed items still applies.
