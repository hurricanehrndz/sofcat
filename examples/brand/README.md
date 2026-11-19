# Rebranded builds

An example brand directory: copy it, edit `brand.env`, add your icons and build
SofCat under your own name.

```sh
mkdir -p branding && cp -R examples/brand branding/acme
just brand=branding/acme setup
```

The guide is <https://sofcat.app/customization/rebrand/>
([source](../../site/src/content/docs/customization/rebrand.md)): the files a
brand directory holds, each `brand.env` key, `brand-check`, the macOS build and
built-in branding from a `branding.yaml`.
