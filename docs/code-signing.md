# Code signing and Defender false positives

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

## How it would plug in

Sign the executables first, then let embala sign the MSI.

1. **Executables.** In the `build` recipe in the `justfile`, after each
   `go build`, run a signing command, kept optional so local builds still
   work unsigned, for example:

       if [ -n "${SOFCAT_SIGN_CMD:-}" ]; then $SOFCAT_SIGN_CMD build/sofcat.exe build/sofcat-ui.exe; fi

   with `SOFCAT_SIGN_CMD` set to something like
   `jsign --storetype TRUSTEDSIGNING --keystore <region>.codesigning.azure.net --storepass "$AZURE_TOKEN" --alias <account>/<profile> --tsaurl http://timestamp.acs.microsoft.com`.
   Always timestamp, so signatures stay valid after the certificate expires.
2. **MSI.** embala 0.7 runs your own tool on each built `.msi` through an
   optional `[sign.windows]` section in `installer/embala.toml`, with `$f` as
   the file (see the embala README, "Signing"):

       [sign.windows]
       command = "jsign ... $f"

   `embala build --no-sign` skips it for local builds.
3. **CI.** In `.github/workflows/build_release.yml`, before the
   "Build executables, MSI and makecatalogs" step, install `jsign` (Java) and
   log in to Azure with OIDC (`azure/login`, using the existing
   `id-token: write` permission) to get an access token, and pass it to the
   build step as a secret environment variable. Keep provenance attestation
   after signing so it covers the signed bytes. SignPath works differently:
   upload the unsigned artifacts with `signpath/github-action-submit-signing-request`
   and attest and release what comes back.

Neither signing nor a WDSI submission has been done yet. Both need the
maintainer's account and identity.
