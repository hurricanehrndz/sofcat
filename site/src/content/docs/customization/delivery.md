---
title: Delivering settings
description: Where SofCat reads branding from, which source wins field by field, how to set it with a registry key or a configuration profile, and how to check the result.
---

The service resolves the storefront's branding and validates it. The UI only
asks for the result with `GetBranding`, and never reads `config.yaml` or the
registry itself. Branding works offline.

This sets a title and an accent colour on Windows:

```bat
reg add "HKLM\SOFTWARE\Policies\SofCat\branding" /v title /t REG_SZ /d "Acme Software Center" /f
reg add "HKLM\SOFTWARE\Policies\SofCat\branding" /v accent /t REG_SZ /d "#0b6e4f" /f
```

The value names are the keys of the `branding:` block in `config.yaml`. What
each key does is on [Branding](/customization/branding/),
[Layout and behaviour](/customization/layout/) and
[Catalog presentation](/customization/catalog/).

## Sources and precedence

Each field comes from the first source that sets it:

1. A `branding.yaml` compiled into a rebranded build. See
   [Built-in branding](/customization/rebrand/#built-in-branding). Stock
   SofCat has none.
2. Managed settings. On Windows these are the values under
   `HKLM\SOFTWARE\Policies\SofCat\branding`. On macOS they are the `branding`
   dict in `/Library/Managed Preferences/<IDENTIFIER>.plist`.
3. The `branding:` block in `config.yaml`.
4. Nothing. The storefront then looks unbranded.

A rebranded build reads `HKLM\SOFTWARE\Policies\<Name>\branding` and its own
plist instead, where `<Name>` is the brand's `NAME`. A profile for one build
never brands another.

Resolution is per field. A policy that sets only `accent` leaves `title` to
`config.yaml`, so you can set the repository in one place and the look in
another. A key you remove from the policy goes back to the next source.

The general rules for managed settings, including the types, the file checks
on macOS and the keys outside `branding`, are in
[Managed settings](/configuration/#managed-settings).

## Windows

Set the values under `HKLM\SOFTWARE\Policies\SofCat\branding`. Use `REG_SZ`
for a string, and `REG_MULTI_SZ` for a list. `sections` is the one list key.

```bat
reg add "HKLM\SOFTWARE\Policies\SofCat\branding" /v nav_placement /t REG_SZ /d left /f
reg add "HKLM\SOFTWARE\Policies\SofCat\branding" /v sections /t REG_MULTI_SZ /d "Apps\0Tools" /f
```

Deliver the same values at scale with Intune, as a custom OMA-URI setting or
a registry script, or with Group Policy Preferences.

## macOS

Deliver a configuration profile whose payload sets the `branding` dict in
your build's preferences domain, `ca.hrndz.sofcat` for stock SofCat. The
keys are the same, and `sections` is an array of strings:

```xml
<key>branding</key>
<dict>
  <key>title</key>
  <string>Acme Software Center</string>
  <key>accent</key>
  <string>#0b6e4f</string>
  <key>sections</key>
  <array>
    <string>Apps</string>
    <string>Tools</string>
  </array>
</dict>
```

SofCat ignores the plist unless root owns it and neither its group nor
others can write it.

## When changes apply

The service reads managed settings at the start of every run, so a policy
change shows after the next run. A `config.yaml` change needs a service
restart.

A value that fails its check is dropped, with a message at debug level only.
The `config.yaml` value does not replace it, so the field shows its
unbranded default.

## Checking the result

Ask the service what it resolved:

```bat
sofcat.exe -S GetBranding
```

The command prints the payload the UI receives, with the logo replaced by
its size in bytes. A field missing from the output was unset or failed its
check.

## How the UI uses it

The UI caches the last payload in the browser's `localStorage` under
`sofcat.branding.v1`. At start it applies the cached payload, then fetches
the current one and applies that.
