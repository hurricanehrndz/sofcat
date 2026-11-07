---
title: Code signing and Defender
description: Why unsigned SofCat binaries can trip Defender's ML heuristics, what the build embeds to help, and how SIGN_CMD signs the Windows executables and installer.
---

## Background

Microsoft Defender has flagged an unsigned `sofcat.exe` as
`Trojan:Win32/Bearfoos.A!ml` on a machine with stale definitions. The `!ml`
suffix marks a machine-learning verdict, not a signature match. After a
definitions update the same file scanned clean. Defender's ML models score
unsigned Go binaries poorly when they also carry no publisher or version
metadata.

`just winres` now embeds a VERSIONINFO resource (company, product, version,
original filename) and an `asInvoker` manifest in both executables. That makes
a false positive less likely but does not rule one out. Two things do more,
and both need the maintainer to act.

## 1. Report the false positive (now, free, per build)

Submit the flagged file at <https://www.microsoft.com/wdsi/filesubmission>:
choose "Software developer", "Incorrectly detected as malware/malicious", and
give the detection name and the release tag. Microsoft usually answers within
a few days and ships a definitions fix. A submission clears that one file hash,
so each release that gets flagged has to be submitted again. Use it to
unblock users now. It is not a lasting fix.

## 2. Authenticode signing (the lasting fix)

What signing changes:

- **Defender and SmartScreen reputation attach to the signing identity, not
  the file hash.** A new release signed by an identity Defender already knows
  inherits that identity's reputation, so ML false positives become much rarer.
  An unsigned build starts with no reputation every time.
- **The UAC and SmartScreen prompts name a publisher** instead of "Unknown
  publisher". Since 2024 an EV certificate no longer skips the SmartScreen
  warning outright. Every identity builds reputation through download volume.
- A self-signed certificate changes nothing for other people's machines.

Options (prices as of 2026; confirm before buying):

| Option | Cost | Effort | Notes |
| --- | --- | --- | --- |
| [Azure Trusted Signing](https://learn.microsoft.com/azure/trusted-signing/) | about US$10 a month (Basic) | Medium: Azure subscription, identity validation (organizations, or individuals in the US/Canada), a CI service principal | Microsoft-run CA, keys stay in Azure, short-lived certificates. `jsign --storetype TRUSTEDSIGNING` signs from Linux. Probably the best fit for this project. |
| [SignPath Foundation](https://signpath.org/) (free for OSS) | Free | Medium: apply, the project must meet their OSS rules (Apache-2.0 qualifies), builds must come from GitHub Actions through their connector, with manual approval per release | The publisher shown is "SignPath Foundation", not you. |
| OV code-signing certificate (Sectigo, DigiCert, SSL.com and others) | about US$200-400 a year | Medium to high: identity validation, and the key must live on an HSM or a cloud key service (CA/B rules since 2023), so CI needs the CA's cloud signing API | Shows your name. |
| EV code-signing certificate | about US$300-600 a year | High: company validation, hardware or cloud key | No longer skips SmartScreen. Worth it only if a customer requires EV. |

## Windows signing hook: `SIGN_CMD`

The build signs when `SIGN_CMD` is set and does nothing about signing when it
is not, so a local `just build` or `just setup` still produces the unsigned
files it always has. `SIGN_CMD` is a shell command line; the build appends the
file to sign and runs it.

- `just build` runs it once, after both `go build`s, with `build/<slug>.exe`
  and `build/<slug>-ui.exe` appended, and fails if the command fails.
- `just setup`, which depends on `build`, appends a `[sign.windows]` section
  with `command = "<SIGN_CMD> $f"` to the `embala.toml` it renders into
  `build/setup/`, so embala runs the same command on the uninstaller stub it
  embeds and then on the finished `setup.exe` (embala README, "Signing").
  `installer/embala.toml` itself carries no sign section.

`SIGN_CMD` is also a `just` variable: `just SIGN_CMD=... setup` is the same as
setting the environment variable, and the command line wins when both are set.

With Azure Trusted Signing, for example:

    SIGN_CMD='jsign --storetype TRUSTEDSIGNING --keystore <region>.codesigning.azure.net --storepass "$AZURE_TOKEN" --alias <account>/<profile> --tsaurl http://timestamp.acs.microsoft.com' just setup

Always timestamp, so signatures stay valid after the certificate expires.

Quoting: `build` runs the value through bash and embala through `sh -c`, so
write it as you would any shell command line. A tool path with a space goes in
single quotes, and `"$AZURE_TOKEN"` expands when the command runs, not when
just echoes the line, so the token stays out of the build log. Double quotes
and backslashes are escaped for the TOML string, so they reach embala as
written. embala quotes the `$f` path itself, and replaces every `$f` in the
command textually, so do not name another variable `$f...` in it. Keep the
value on one line: a newline splits the `build` line and breaks the TOML
string. embala uses `sh -c` on Linux and macOS hosts, which is where `setup`
runs in CI; on a Windows host it uses `cmd /C`, where `"$AZURE_TOKEN"` would
not expand.

Verify a signed file with `osslsigncode verify build/sofcat.exe` on Linux or
`Get-AuthenticodeSignature .\sofcat.exe` on Windows (Status `Valid`), and the
same for `setup.exe`.

In CI, set `SIGN_CMD` in the environment of the "Build executables, setup.exe
and makecatalogs" step of `.github/workflows/build_release.yml`. For Trusted
Signing, install `jsign` (Java) and log in to Azure with OIDC (`azure/login`,
using the existing `id-token: write` permission) first, and pass the token as
a secret environment variable. Keep provenance attestation after signing so it
covers the signed bytes. SignPath works differently: upload the unsigned
artifacts with `signpath/github-action-submit-signing-request` and attest and
release what comes back.

No Windows signing credential exists for this project yet, and no WDSI
submission has been made. Both need the maintainer's account and identity.
