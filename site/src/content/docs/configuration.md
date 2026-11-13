---
title: Configuration
description: Where SofCat's config lives and the annotated example files for config, manifests, catalogs and package info.
---

The agent reads one file, `config.yaml`:

| Platform | Path |
| --- | --- |
| Windows | `C:\ProgramData\SofCat\config.yaml` |
| macOS | `/Library/Application Support/SofCat/config.yaml` (root-owned, `0600`) |

A change to this file takes effect when the service restarts. The minimum is the
repository URL, the machine's manifest and the catalogs to search:

```yaml
url: https://example.com/sofcat/
manifest: example_manifest
catalogs:
  - example_catalog
```

Managed settings from MDM or Group Policy override this file key by key, and
can replace it entirely. See [Managed settings](#managed-settings).

## Managed settings

Most `config.yaml` keys can also come from managed policy. A managed value
wins over the same key in `config.yaml`; keys the policy leaves out, or sets
to an empty string or empty list, keep the file's value. A list replaces the
file's list whole. When policy supplies `url` and `manifest`, `config.yaml`
is optional.

The service reads policy at the start of every run, so a policy change needs
no restart. It applies from the next run, and a run under way finishes with
the settings it started with. A key you remove from the policy goes back to
the file's value. A new `service_interval` applies after the next scheduled
run.

| Platform | Where | Types |
| --- | --- | --- |
| Windows | Values under `HKLM\SOFTWARE\Policies\SofCat`; branding under its `branding` subkey | `REG_SZ` or `REG_EXPAND_SZ` (environment variables are expanded) for strings, `REG_MULTI_SZ` for lists, `REG_DWORD` for booleans (non-zero is true) |
| macOS | Top-level keys of `/Library/Managed Preferences/ca.hrndz.sofcat.plist`, delivered by a configuration profile; branding as a `branding` dict | `string`, `array` of strings, `true`/`false` or `integer` |

A rebranded build reads `HKLM\SOFTWARE\Policies\<NAME>` and its own
`<IDENTIFIER>.plist`. Value names are the YAML key names; the registry
matches them case-insensitively. On macOS the plist is ignored unless root
owns it and neither its group nor others can write it.

| Key | Managed |
| --- | --- |
| `url`, `url_packages`, `manifest`, `local_manifests`, `catalogs` | Yes |
| `verbose`, `debug`, `log_file_plain`, `checkonly`, `service_interval` | Yes |
| `tls_auth`, `tls_client_cert`, `tls_client_key`, `tls_server_cert` (file paths) | Yes |
| `tls_client_cert_store`, `tls_client_cert_store_field` | Yes |
| `branding` (`title`, `tagline`, `logo`, `help_url`, `help_label`, `accent`, `list_heading`, `sections`) | Yes, field by field |
| `auth_user`, `auth_pass` | No: ignored with a warning in `sofcat.log` |
| `app_data_path`, `service_name`, `service_pipe_name` | No: they must match the installed build |

A value of the wrong type is dropped with a warning and the rest of the
policy still applies. The `WriteRoots` policy lives in the same places; see
[Write roots](#write-roots).

On Windows, for example:

```powershell
$key = 'HKLM:\SOFTWARE\Policies\SofCat'
New-Item -Path $key -Force | Out-Null
Set-ItemProperty -Path $key -Name url -Value 'https://sofcat.example.com/repo/'
Set-ItemProperty -Path $key -Name manifest -Value 'site_default'
New-ItemProperty -Path $key -Name catalogs -PropertyType MultiString -Value @('production') -Force | Out-Null
New-ItemProperty -Path $key -Name tls_auth -PropertyType DWord -Value 1 -Force | Out-Null
Restart-Service sofcat
```

### Credentials

Policy never carries `auth_user` or `auth_pass`. Both policy stores are
readable by every local user, while `config.yaml` is readable only by SYSTEM
and Administrators (root on macOS).

Basic auth with `auth_user` and `auth_pass` gives every machine the same
shared secret, and it ends up in plain text in each machine's config. We
strongly advise against using it for a repository reachable from the
internet. Use mutual TLS instead:

- Issue each machine its own client certificate, from your MDM's certificate
  payload, SCEP or ACME, or your PKI's enrollment agent. The private key
  stays on the machine and you can revoke one machine without touching the
  rest.
- Set `tls_auth` and `tls_client_cert_store`, a regular expression that
  picks the certificate from the machine store: `LocalMachine\My` on
  Windows, the System keychain on macOS. It matches the subject's common
  name, or the field `tls_client_cert_store_field` names: `issuer` (its
  common name), `serial` or `dns_names` (any of them). Of the matching
  certificates SofCat presents the one valid now that expires last, so a
  renewed certificate replaces the old one at the next run without a
  restart. The key never leaves the store, so a non-exportable or
  TPM-backed key works. On macOS the key's access control must let the
  daemon use it, since a daemon cannot answer the keychain's prompt: set
  `AllowAllAppsAccess` in the certificate payload of the MDM profile
  (PKCS #12, SCEP or ACME), or import it by hand with `security import -A`.
  Without it the log shows `CFError -25308` with that advice.
- Or point `tls_client_cert` and `tls_client_key` at PEM files. Restrict the
  key file to SYSTEM and Administrators (root on macOS).
- `tls_server_cert` is a PEM file of the roots that verify the repository.
  Leave it out to use the system's. Policy can set every one of these keys;
  none is a secret.
- Terminate TLS at a reverse proxy or CDN that requires a client certificate
  from your CA in front of a static repository.

## Example files

The repository's [`examples/`](https://github.com/hurricanehrndz/sofcat/tree/main/examples)
directory has an annotated file for each format. They are the reference for
every key.

| File | What it shows |
| --- | --- |
| [`example_config.yaml`](https://github.com/hurricanehrndz/sofcat/blob/main/examples/example_config.yaml) | The agent's `config.yaml`: repository, credentials, service settings and the `branding:` block. |
| [`example_manifest.yaml`](https://github.com/hurricanehrndz/sofcat/blob/main/examples/example_manifest.yaml) | A manifest: managed, optional and default installs, updates, uninstalls and `conditional_items`. |
| [`example_catalog.yaml`](https://github.com/hurricanehrndz/sofcat/blob/main/examples/example_catalog.yaml) | A catalog as `makecatalogs` writes it: installers, checks, dependencies, action labels and required software. |
| [`example_package-info.yaml`](https://github.com/hurricanehrndz/sofcat/blob/main/examples/example_package-info.yaml) | One item's package info, the input to `makecatalogs`. |
| [`placements.yaml`](https://github.com/hurricanehrndz/sofcat/blob/main/examples/placements.yaml) | Placements for zip items. See [Zip items](/zip-items/overview/). |
| [`zip-items/`](https://github.com/hurricanehrndz/sofcat/tree/main/examples/zip-items) | Two zip items' sources, item trees and generated package-info files. |
| [`brand/`](https://github.com/hurricanehrndz/sofcat/tree/main/examples/brand) | A rebrand: your own name, icon, service and installer. |

## Branding

The storefront's title, tagline, logo, help link, accent colour, list heading
and section tab order come from the `branding:` block in `config.yaml` or from
[managed settings](#managed-settings), with no rebuild. The keys and their
limits are in the UI's
[branding reference](https://github.com/hurricanehrndz/sofcat/blob/main/ui/README.md#branding).

To ship SofCat under your own name, icon, service and installer instead,
build a rebrand from
[`examples/brand/`](https://github.com/hurricanehrndz/sofcat/tree/main/examples/brand).

## Write roots

Zip items are placed only under write roots, which the build sets. Policy can
add more, never through `config.yaml`: on Windows the `REG_MULTI_SZ` values
`Machine` and `User` under `HKLM\SOFTWARE\Policies\SofCat\WriteRoots`, on
macOS the `WriteRoots` dictionary in
`/Library/Managed Preferences/ca.hrndz.sofcat.plist`. See
[Write roots](/zip-items/placements/#write-roots).

## Conditions

Items can be limited to machines whose facts match an expression, in a
catalog's `installable_condition` or a manifest's `conditional_items`. See
[Installable conditions](/guides/conditions/).
