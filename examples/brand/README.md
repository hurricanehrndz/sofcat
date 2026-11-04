# Rebranded builds

`just brand=<dir> build` (or `msi`) builds SofCat under another name, the way
[munki-rebrand](https://github.com/TSPARR/munki-rebrand) does for Munki. `<dir>`
is relative to the checkout or absolute, and may live outside it. Keep
brands under `branding/`, which git ignores:

```sh
mkdir -p branding && cp -R examples/brand branding/acme
# edit branding/acme/brand.env, add the icons
just brand=branding/acme msi   # -> build/acme-apps-<version>-x86_64.msi
```

`<dir>` holds:

| File | Required | Used for |
|---|---|---|
| `brand.env` | yes | Names, publisher, identifier and installer allowlist; see the comments in [`brand.env`](brand.env). |
| `icon.ico` | no | Exe icon (Explorer, Start menu, Add/Remove Programs). Defaults to `assets/sofcat.ico`. |
| `icon.png` | no | Window and taskbar icon (256x256 is best) and the app-bar mark when no runtime logo is set. Defaults to the SofCat icon. |
| `write-roots.yaml` | no | Where zip items may be placed, compiled in; see [docs/zip-items.md](../../docs/zip-items.md#write-roots). Defaults to `${ProgramFiles}\<NAME>` (Windows) and `/Library/Application Support/<NAME>` (macOS). |

Setting `INSTALLERS=zip` in `brand.env` makes both binaries zip-only: they
contain no command installer (`msi`, `exe`, `ps1`, `msix`, `nupkg`) and run no
catalog script, and they reject catalog items that need either; see
[docs/zip-items.md](../../docs/zip-items.md#zip-only-builds). `brand-check`
rejects any other value.

`just brand-check` validates the values on its own; every brand build runs it
first.

A rebranded build is a separate product: its service, pipe, data folder and
policy key derive from `SLUG` and `NAME` (`pkg/identity`), so it installs beside
stock SofCat or another brand without sharing any of them, as long as each
brand's `SLUG`, `NAME` and `IDENTIFIER` differ. Its runtime branding (title,
logo, accent, help link) still comes from `config.yaml` or
`HKLM\SOFTWARE\Policies\<NAME>\Branding`, as described in
[ui/README.md](../../ui/README.md#branding).
