# Rebranded builds

`just brand=<dir> build` (or `setup`) builds SofCat under another name, the way
[munki-rebrand](https://github.com/TSPARR/munki-rebrand) does for Munki. `<dir>`
is relative to the checkout or absolute, and may live outside it. Keep
brands under `branding/`, which git ignores:

```sh
mkdir -p branding && cp -R examples/brand branding/acme
# edit branding/acme/brand.env, add the icons
just brand=branding/acme setup   # -> build/acme-apps-<version>-x86_64-setup.exe
```

`<dir>` holds:

| File | Required | Used for |
|---|---|---|
| `brand.env` | yes | Names, publisher, identifier and installer allowlist; see the comments in [`brand.env`](brand.env). |
| `icon.ico` | no | Exe icon (Explorer, Start menu, Add/Remove Programs). Defaults to `assets/sofcat.ico`. |
| `icon.png` | no | Window and taskbar icon (256x256 is best) and the app-bar mark when no runtime logo is set. Defaults to the SofCat icon. |
| `write-roots.yaml` | no | Where zip items may be placed, compiled in; see [Zip items](../../site/src/content/docs/zip-items/placements.md#write-roots). Defaults to `${ProgramData}\<NAME>` (Windows) and `/Library/Application Support/<NAME>` (macOS), the directory holding the config and `Data`. Managed policy can add more. |
| `branding.yaml` | no | Storefront branding compiled into the service, which no config or policy overrides; see [Built-in branding](#built-in-branding). |

Setting `INSTALLERS=zip` in `brand.env` makes all three binaries zip-only: they
contain no command installer (`msi`, `exe`, `ps1`, `msix`, `nupkg`) and reject
catalog items that need one; see
[Zip items](../../site/src/content/docs/zip-items/building.md#zip-only-builds). `brand-check`
rejects any other value.

`just brand-check` validates the values on its own; every brand build runs it
first.

On a Mac, `just brand=<dir> build-macos pkg` builds the same brand as
`build/<SLUG>` and `build/<NAME>.app` (bundle identifier `IDENTIFIER`, icon
made from `icon.png`) and wraps them in an unsigned `build/<SLUG>-<version>.pkg`
that installs a launchd daemon labelled `IDENTIFIER`; see `AGENTS.md`.

A rebranded build is a separate product: its service, pipe, data folder and
policy key derive from `SLUG` and `NAME` (`pkg/identity`), so it installs beside
stock SofCat or another brand without sharing any of them, as long as each
brand's `SLUG`, `NAME` and `IDENTIFIER` differ. Its runtime branding (title,
logo, accent, help link) still comes from `config.yaml` or
`HKLM\SOFTWARE\Policies\<NAME>\branding` (macOS: managed preferences), as described in
[ui/README.md](../../ui/README.md#branding), except for the fields the build
fixes with a `branding.yaml`.

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

A field set here is fixed: it wins over `config.yaml` and managed settings,
and changing it takes a new build. A field left out still comes from managed
settings or `config.yaml`, so set only what every deployment of the build
must share. A deployment can take its `url` and `manifest` from MDM and its
look from the build. Only branding keys are allowed; `brand-check` fails on
any other key, so a build never carries a repository URL.

`logo` and `background_image` are paths on the managed machine, and the build
installs no file there. Leave them out unless your own packaging puts the
images in place; the brand's `icon.png` is already the app-bar mark when no
logo is set.
