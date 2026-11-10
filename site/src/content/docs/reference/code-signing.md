---
title: Code signing and Defender
description: Why unsigned SofCat binaries can trip Defender's ML heuristics, what the build embeds to help, how SIGN_CMD signs the Windows executables and installer, and how the macOS app and pkg are signed and notarized.
---

## Background

Microsoft Defender has flagged an unsigned `sofcat.exe` as
`Trojan:Win32/Bearfoos.A!ml` on a machine with stale definitions. The `!ml`
suffix marks a machine-learning verdict, not a signature match. After a
definitions update the same file scanned clean. Defender's ML models score
unsigned Go binaries poorly when they also carry no publisher or version
metadata.

`just winres` now embeds a VERSIONINFO resource (company, product, version,
original filename) and an `asInvoker` manifest in all three executables. That makes
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

- `just build` runs it once, after the three `go build`s, with `build/<slug>.exe`,
  `build/<slug>-ui.exe` and `build/<slug>-useragent.exe` appended, and fails if the command fails.
- `just makecatalogs` runs it once, after its builds, with
  `build/makecatalogs-windows-amd64.exe` and
  `build/makecatalogs-windows-arm64.exe` appended.
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

## Release signing in CI

`.github/workflows/build_release.yml` signs every release with Azure Trusted
Signing. It downloads a pinned `jsign` jar, logs the Azure CLI in as a service
principal, takes a token for `https://codesigning.azure.net` and sets
`SIGN_CMD` for the build step, so the three executables, the uninstaller stub,
`setup.exe` and the two Windows `makecatalogs` are signed and timestamped, with the description `SofCat` and
the URL `https://sofcat.app`. Provenance attestation runs afterwards, so it
covers the signed bytes. The repository needs:

| Kind | Name | Value |
| --- | --- | --- |
| Secret | `AZURE_TENANT_ID` | The Entra tenant of the service principal. |
| Secret | `AZURE_CLIENT_ID` | Its application (client) ID. |
| Secret | `AZURE_CLIENT_SECRET` | A client secret for it. |
| Variable | `TRUSTED_SIGNING_ENDPOINT` | The account's regional endpoint host, such as `eus.codesigning.azure.net`. |
| Variable | `TRUSTED_SIGNING_ACCOUNT` | The Trusted Signing account name. |
| Variable | `TRUSTED_SIGNING_PROFILE` | The certificate profile name. |

The service principal needs the "Trusted Signing Certificate Profile Signer"
role on the certificate profile. A release is never cut unsigned: the build
step fails when any of the six is missing. The Windows integration job that
follows then checks each released `.exe` with `Get-AuthenticodeSignature` and
fails unless every Status is `Valid`.

The client secret is a long-lived credential that has to be rotated before it
expires. A federated credential (`azure/login` with OIDC, using the job's
`id-token: write` permission) would remove it.

`Azure/trusted-signing-action` is not used: it needs a Windows runner and
signs finished files, so it cannot sign the uninstaller stub that embala
embeds in `setup.exe`, nor the executables before they are packed into it.

SignPath would work differently: upload the unsigned artifacts with
`signpath/github-action-submit-signing-request` and attest and release what
comes back.

No WDSI submission has been made; it needs the maintainer's account.

## macOS signing and notarization

Gatekeeper refuses an app or pkg downloaded from the web unless it is signed
with a Developer ID and notarized by Apple. `just build-macos` and `just pkg`
sign and notarize when these variables are set and do nothing about signing
when they are not, so a local `just pkg` still produces the unsigned pkg it
always has. Each is also a `just` variable, like `SIGN_CMD`: `just
CODESIGN_IDENTITY=... pkg` is the same as setting the environment variable,
and the command line wins when both are set.

| Variable | Read by | Value |
| --- | --- | --- |
| `CODESIGN_IDENTITY` | `build-macos` | The `codesign` identity for the CLI and the app: a Developer ID Application certificate's name or SHA-1, or `-` for an ad-hoc signature. |
| `PRODUCTSIGN_IDENTITY` | `pkg` | The Developer ID Installer identity for `productbuild --sign`. Needs `CODESIGN_IDENTITY`. |
| `NOTARIZE` | `pkg` | `1` runs `just notarize` on the pkg once it is built. Needs both identities and the four variables below; any other value is an error. |
| `NOTARY_AUTH` | `notarize` | `api-key`, the only mode: an App Store Connect API key. |
| `NOTARY_API_KEY_PATH` | `notarize` | The key's `.p8` file. |
| `NOTARY_API_KEY_ID` | `notarize` | The key ID. |
| `NOTARY_API_ISSUER_ID` | `notarize` | The issuer ID. |

`just build-macos` codesigns `build/<slug>` right after its `go build`, with
the identifier `<IDENTIFIER>.cli`, and `build/<Name>.app` as a bundle once
its `Info.plist` and icon are in place, since the signature seals them along
with the main executable. Both get `--options runtime` (the hardened runtime,
which notarization requires) and `--timestamp`, except that the ad-hoc
identity gets no timestamp, which `codesign` refuses for it. `--force`
replaces the signature of an earlier build. No entitlements: the Wails app
runs under the hardened runtime without any.

`just pkg` checks the variables before anything is built: a
`PRODUCTSIGN_IDENTITY` without `CODESIGN_IDENTITY`, or `NOTARIZE=1` without
either identity or one of the notary variables, fails naming the missing
one. It passes `--sign` to `productbuild` only; the three component pkgs stay
unsigned, since the product signature covers them. With `NOTARIZE=1` it
then runs `just notarize build/<slug>-<version>.pkg`.

`just notarize <pkg>` runs `xcrun notarytool submit <pkg> --key <path>
--key-id <id> --issuer <issuer> --wait`, then `xcrun stapler staple <pkg>`
and `xcrun stapler validate <pkg>`, and stops at the first that fails. It
does not check that the key file exists; `notarytool` reports that itself.
Apple refuses an ad-hoc signature, so a pkg for notarization needs real
identities for both variables.

Verify each stage:

    codesign --verify --strict build/sofcat
    codesign --verify --strict --deep build/SofCat.app
    codesign -d --verbose=2 build/SofCat.app 2>&1 | grep runtime        # flags=0x10000(runtime)
    pkgutil --check-signature build/sofcat-<version>.pkg                 # Status: signed by ...
    xcrun stapler validate build/sofcat-<version>.pkg                    # The validate action worked
    spctl --assess --type install -v build/sofcat-<version>.pkg          # accepted, source=Notarized Developer ID

With a Developer ID, for example (`security find-identity -v` lists the
identities in the keychain):

    CODESIGN_IDENTITY='Developer ID Application: Name (TEAMID)' PRODUCTSIGN_IDENTITY='Developer ID Installer: Name (TEAMID)' NOTARIZE=1 NOTARY_AUTH=api-key NOTARY_API_KEY_PATH=~/AuthKey_ABC123DEF.p8 NOTARY_API_KEY_ID=ABC123DEF NOTARY_API_ISSUER_ID=<issuer uuid> just pkg

No macOS signing credential exists for this project yet either; it needs the
maintainer's Apple Developer account.
