---
title: Rebranded builds
description: Build SofCat under your own name, icon, service and installer, set the brand's identity in brand.env, and compile in storefront branding.
---

A rebranded build ships SofCat under your own name, with its own icon,
service, installer and policy key. The idea is the one
[munki-rebrand](https://github.com/TSPARR/munki-rebrand) applies to Munki. The
result is a separate product, so you need a rebrand only when settings are not
enough. For the look alone, see [Branding](/customization/branding/).

A brand is a directory. Copy the example from
[`examples/brand`](https://github.com/hurricanehrndz/sofcat/tree/main/examples/brand),
edit it and build:

```sh
mkdir -p branding && cp -R examples/brand branding/acme
# edit branding/acme/brand.env, add the icons
just brand=branding/acme setup   # -> build/acme-apps-<version>-x86_64-setup.exe
```

`just brand=<dir> build` builds the three executables without the installer.
`<dir>` is relative to the checkout or absolute, and may live outside it.
Keep brands under `branding/`, which git ignores.

## The brand directory

| File | Required | Used for |
|---|---|---|
| `brand.env` | yes | Names, publisher, identifier and installer allowlist; see [`brand.env`](#brandenv). |
| `icon.ico` | no | Exe icon (Explorer, Start menu, Add or remove programs). Defaults to the SofCat icon. |
| `icon.png` | no | Window and taskbar icon (256x256 is best) and the app-bar mark when no runtime logo is set. Defaults to the SofCat icon. |
| `write-roots.yaml` | no | Where zip items may be placed, compiled in. Defaults to `${ProgramData}\<NAME>` on Windows and `/Library/Application Support/<NAME>` on macOS, the directory holding the config and `Data`. Managed policy can add more. See [Write roots](/zip-items/placements/#write-roots). |
| `branding.yaml` | no | Storefront branding compiled into the service, which no config or policy overrides. See [Built-in branding](#built-in-branding). |

## `brand.env`

A shell file that the build sources. Every key is optional and falls back to
stock SofCat. `SLUG`, `NAME` and `IDENTIFIER` must all change together,
because the service, pipe, ProgramData folder, policy key, installer
identity and launchd label derive from them.

```sh
SLUG=acme-apps
NAME="Acme Software Center"
IDENTIFIER=com.example.softwarecenter
PUBLISHER="Acme Corp"
DESCRIPTION="Install and update Acme-approved software"
HOMEPAGE=https://help.example.com/software
#INSTALLERS=zip
```

### `SLUG`

The service name, the pipe prefix (`<slug>-service`), the executable names
(`<slug>.exe`, `<slug>-ui.exe` and `<slug>-useragent.exe`) and the
`%LocalAppData%` folder. Use 2 to 32 characters from `a-z`, `0-9` and `-`.

### `NAME`

The display name. It appears in the Start menu, Add or remove programs, the
window title and the version resource. It also names `C:\Program Files\<NAME>`,
`C:\ProgramData\<NAME>` and the branding policy key
`HKLM\SOFTWARE\Policies\<NAME>\branding`.

### `IDENTIFIER`

A reverse-DNS string. It is the installer's package identifier and the macOS
launchd label, bundle identifier and package identifier, so keep it stable
across releases.

### `PUBLISHER`

The publisher name in the installer and in Add or remove programs.

### `DESCRIPTION`

A one-line description, in the installer metadata and on the Start-menu shortcut.

### `HOMEPAGE`

The product's web page, in the installer metadata. It must be an http or https URL.

### `INSTALLERS`

An installer allowlist. Unset builds every installer type. `zip` builds all
three binaries zip-only: they contain no command installer (`msi`, `exe`,
`ps1`, `msix`, `nupkg`) and reject catalog items that need one. Catalog
scripts stay in every build. `brand-check` rejects any other value. See
[Zip-only builds](/zip-items/building/#zip-only-builds).

## Checking a brand

`just brand-check` validates the values on its own. Every brand build runs it
first.

## Building on macOS

`just brand=<dir> build-macos pkg` builds the same brand as `build/<SLUG>`
and `build/<NAME>.app`, with `IDENTIFIER` as the bundle identifier and the
icon made from `icon.png`. It wraps them in an unsigned
`build/<SLUG>-<version>.pkg` that installs a launchd daemon labelled
`IDENTIFIER`. To sign and notarise the result, see
[Code signing](/reference/code-signing/).

## A separate product

A rebranded build shares nothing with stock SofCat. Its service, pipe, data
folder and policy key derive from `SLUG` and `NAME`, so it installs beside
stock SofCat or another brand, as long as each brand's `SLUG`, `NAME` and
`IDENTIFIER` differ.

Its runtime branding still comes from `config.yaml` or from managed settings,
under `HKLM\SOFTWARE\Policies\<NAME>\branding` on Windows and its own
managed preferences plist on macOS. The exception is any field the build
fixes with a `branding.yaml`. See
[Delivering settings](/customization/delivery/).

## Built-in branding

A `branding.yaml` in the brand directory gives the build its own storefront
branding, with no `config.yaml` or policy needed. It holds the keys of
`config.yaml`'s `branding:` block at the top level:

```yaml
title: Acme Software Center
tagline: Install and update Acme-approved software
accent: "#0a66c2"
link_url: https://help.example.com/software
link_label: Get help
sections: [Apps, Tools]
```

A field set here is fixed. It wins over `config.yaml` and managed settings,
and changing it takes a new build. A field left out still comes from managed
settings or `config.yaml`, so set only what every deployment of the build
must share. A deployment can take its `url` and `manifest` from MDM and its
look from the build.

Only branding keys are allowed. `brand-check` fails on any other key, so a
build never carries a repository URL.

`logo` and `background_image` are paths on the managed machine, and the build
installs no file there. Leave them out unless your own packaging puts the
images in place. The brand's `icon.png` is already the app-bar mark when no
logo is set.
