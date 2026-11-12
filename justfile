# sofcat task runner. All build artifacts go to build/.
# Tools come from mise.toml (`mise install`); `just dev-setup` wires the git hook.
set shell := ["bash", "-cu"]

# Rebranding: `just brand=branding/acme build` (or setup) takes the values below
# from <brand>/brand.env instead of the stock SofCat ones. <brand> is relative
# to this file or absolute, and may be outside the checkout; see
# examples/brand/README.md. pkg/identity defaults to the same stock values for
# builds that bypass just.
brand := ""
_brand_env := if brand == "" { "" } else { brand / "brand.env" }
_read := 'unset SLUG NAME PUBLISHER IDENTIFIER DESCRIPTION HOMEPAGE INSTALLERS; [ -z "$1" ] || . "$1"; printf %s "${!2:-$3}"'
app := shell(_read, _brand_env, "SLUG", "sofcat")
product := shell(_read, _brand_env, "NAME", "SofCat")
publisher := shell(_read, _brand_env, "PUBLISHER", "Carlos Hernandez")
identifier := shell(_read, _brand_env, "IDENTIFIER", "ca.hrndz.sofcat")
description := shell(_read, _brand_env, "DESCRIPTION", "A catalog for anything you can put on disk or run")
homepage := shell(_read, _brand_env, "HOMEPAGE", "https://github.com/hurricanehrndz/sofcat")
# INSTALLERS=zip builds the Windows binaries with the zip_only tag: no command
# installers or catalog scripts are compiled in (site/src/content/docs/zip-items/building.md).
installers := shell(_read, _brand_env, "INSTALLERS", "")
_zip_tag := if installers == "zip" { ",zip_only" } else { "" }
_brand_ico := if brand == "" { "" } else { brand / "icon.ico" }
_brand_png := if brand == "" { "" } else { brand / "icon.png" }
icon_ico := if path_exists(_brand_ico) == "true" { _brand_ico } else { "assets/sofcat.ico" }
icon_png := if path_exists(_brand_png) == "true" { _brand_png } else { "" }
# A brand's write-roots.yaml (where zip items may be placed) is compiled into
# the binaries under the brand_roots tag; without it the stock roots apply.
_brand_roots := if brand == "" { "" } else { brand / "write-roots.yaml" }
write_roots := if path_exists(_brand_roots) == "true" { _brand_roots } else { "" }
_svc_tags := trim_start_match((if write_roots == "" { "" } else { "brand_roots" }) + _zip_tag, ",")
_ui_icon_tag := if icon_png == "" { "" } else { ",brand" }
_ui_tags := "production" + _ui_icon_tag + (if write_roots == "" { "" } else { ",brand_roots" }) + _zip_tag
# sed arguments filling the @PLACEHOLDERS@ in installer/* and
# */winres/winres.json. brand-check keeps the values free of ' | & \ and ".
_subst := "-e 's|@APP@|" + app + "|g' -e 's|@PRODUCT@|" + product + "|g' -e 's|@PUBLISHER@|" + publisher + "|g' -e 's|@IDENTIFIER@|" + identifier + "|g' -e 's|@DESCRIPTION@|" + description + "|g' -e 's|@HOMEPAGE@|" + homepage + "|g'"
_identity := "-X github.com/hurricanehrndz/sofcat/pkg/identity.Slug=" + app + " -X 'github.com/hurricanehrndz/sofcat/pkg/identity.Name=" + product + "' -X github.com/hurricanehrndz/sofcat/pkg/identity.Identifier=" + identifier
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
# Signing hook (site/src/content/docs/reference/code-signing.md): a shell
# command line that `build` runs with the three executables appended,
# `makecatalogs` with its two Windows ones, and `setup` hands to embala as
# [sign.windows] for setup.exe and the uninstaller stub.
# Unset, nothing is signed.
SIGN_CMD := env("SIGN_CMD", "")
# macOS signing (same page). With CODESIGN_IDENTITY set, `build-macos`
# codesigns the CLI and the app bundle with it (hardened runtime; `-` is the
# ad-hoc identity); with PRODUCTSIGN_IDENTITY set, `pkg` signs the product
# archive; with NOTARIZE=1, `pkg` then runs `notarize` on it, which needs
# NOTARY_AUTH=api-key and an App Store Connect API key. Unset, nothing is
# signed or notarized.
CODESIGN_IDENTITY := env("CODESIGN_IDENTITY", "")
PRODUCTSIGN_IDENTITY := env("PRODUCTSIGN_IDENTITY", "")
NOTARIZE := env("NOTARIZE", "")
NOTARY_AUTH := env("NOTARY_AUTH", "")
NOTARY_API_KEY_PATH := env("NOTARY_API_KEY_PATH", "")
NOTARY_API_KEY_ID := env("NOTARY_API_KEY_ID", "")
NOTARY_API_ISSUER_ID := env("NOTARY_API_ISSUER_ID", "")
# The first notary variable a run would lack, or empty.
_notary_missing := if NOTARY_AUTH != "api-key" { "NOTARY_AUTH=api-key" } else if NOTARY_API_KEY_PATH == "" { "NOTARY_API_KEY_PATH" } else if NOTARY_API_KEY_ID == "" { "NOTARY_API_KEY_ID" } else if NOTARY_API_ISSUER_ID == "" { "NOTARY_API_ISSUER_ID" } else { "" }
# No --timestamp for the ad-hoc identity: codesign refuses to timestamp it.
_codesign := "codesign --force --options runtime" + (if CODESIGN_IDENTITY == "-" { "" } else { " --timestamp" }) + " --sign " + quote(CODESIGN_IDENTITY)
pkg_version := replace_regex(replace_regex(replace_regex(version, "^v", ""), "-([0-9]+)-g([0-9a-f]+)(-dirty)?$", "+${1}.g${2}${3}"), "^([^+]*)-dirty$", "${1}+dirty")
manual_test_dir := "build/manual-test"
server_root := manual_test_dir / "server-root"
vm_dir := manual_test_dir / "vm"

# List recipes.
default:
    @just --list

# One-time contributor setup: frontend deps and the prek pre-commit hook
# (prek.toml). --force replaces a hook left by an older setup.
dev-setup: ui-install
    prek install --force

# Every pre-commit hook, on every file rather than only staged ones.
pre-commit:
    prek run --all-files

# Frontend dependencies.
ui-install:
    npm ci --prefix ui/frontend

# Frontend type check.
ui-type: ui-install
    npm run check --prefix ui/frontend

# Frontend tests.
ui-test: ui-install
    npm test --prefix ui/frontend

# Production frontend assets. A brand icon.png replaces the app-bar mark in a
# copy of public/ (build/brand/public).
ui-assets: ui-install brand-check
    rm -rf build/brand/public
    @if [ -n "{{icon_png}}" ]; then \
      mkdir -p build/brand && cp -R ui/frontend/public build/brand/public && \
      cp "{{icon_png}}" build/brand/public/sofcat-icon.png; fi
    VITE_PRODUCT_NAME="{{product}}" SOFCAT_PUBLIC_DIR="{{ if icon_png == "" { "" } else { absolute_path("build/brand/public") } }}" \
        npm run build --prefix ui/frontend
    @if grep -rq SOFCAT_VITE_MOCK_ONLY ui/frontend/dist; then \
      echo "Dev mock leaked into the production bundle" && exit 1; \
      else echo "Production bundle is mock-free"; fi

# Verify committed Wails bindings.
ui-bindings-check:
    rm -rf build/ui-bindings-check
    mkdir -p build
    cd ui && go run github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0 generate bindings -clean -ts -noevents -d ../build/ui-bindings-check .
    diff -ru ui/frontend/bindings build/ui-bindings-check

# Type check plus committed-binding verification.
ui-lint: ui-type ui-bindings-check

# Documentation site dependencies.
site-install:
    npm ci --prefix site

# Documentation site with live reload, without the storefront demo (`just site`).
site-dev: site-install
    npm run dev --prefix site

# The storefront demo (npm run build:demo, ui/frontend/dist-demo) is copied
# into the Astro build, so astro dev and the link check never see /demo/.
# Documentation site in site/dist, with the storefront demo at site/dist/demo/.
site: site-install ui-install
    rm -rf ui/frontend/dist-demo
    npm run build:demo --prefix ui/frontend
    npm run build --prefix site
    @if [ ! -f ui/frontend/dist-demo/index.html ]; then \
      echo "ui/frontend/dist-demo/index.html is missing: npm run build:demo wrote no demo" >&2 && exit 1; fi
    rm -rf site/dist/demo
    cp -R ui/frontend/dist-demo site/dist/demo

# Windows resources (rsrc_windows_<arch>.syso, gitignored) that go build links
# into each executable: icon, VERSIONINFO (company, product, and file and
# product version from `version`) and an asInvoker application manifest, all
# from <pkg>/winres/winres.json. Unsigned binaries without these look anonymous
# to antivirus ML heuristics (site/src/content/docs/reference/code-signing.md). The UI manifest declares
# what Wails' own template does: per-monitor-v2 DPI and common controls v6.
# Each template is rendered with the brand values beside a copy of the icon
# under build/winres/<pkg>/.
winres arch="amd64": brand-check
    rm -rf build/winres
    for dir in cmd/sofcat cmd/sofcat-useragent ui; do \
      out=build/winres/$dir && mkdir -p $out && cp "{{icon_ico}}" $out/icon.ico && \
      sed {{_subst}} $dir/winres/winres.json > $out/winres.json && \
      go run github.com/tc-hib/go-winres@v0.3.3 make --arch {{arch}} --in $out/winres.json \
        --file-version "{{version}}" --product-version "{{version}}" --out $dir/rsrc || exit 1; \
    done

# Windows binaries (pure Go, no cgo) -> build/<app>.exe, build/<app>-ui.exe and
# build/<app>-useragent.exe, the per-user helper (cmd/sofcat-useragent).
# A brand icon.png is staged as ui/brand-icon.png (gitignored) for the brand tag,
# and a brand write-roots.yaml as pkg/placement/brand-write-roots.yaml
# (gitignored) for the brand_roots tag.
# A brand with INSTALLERS=zip adds the zip_only tag to all three.
# -s -w drops the symbol table and DWARF, about 30% of each file; panics
# still print file:line stack traces, but Delve and symbolized profiles
# need a build without them.
build arch="amd64": ui-assets (winres arch)
    mkdir -p build
    {{ if write_roots == "" { "true" } else { "cp '" + write_roots + "' pkg/placement/brand-write-roots.yaml" } }}
    GOOS=windows GOARCH={{arch}} CGO_ENABLED=0 \
        go build -tags "{{_svc_tags}}" -ldflags "-s -w -X github.com/hurricanehrndz/sofcat/pkg/version.appName={{app}} -X github.com/hurricanehrndz/sofcat/pkg/version.version={{version}} {{_identity}}" \
        -o build/{{app}}.exe ./cmd/sofcat
    {{ if icon_png == "" { "true" } else { "cp '" + icon_png + "' ui/brand-icon.png" } }}
    GOOS=windows GOARCH={{arch}} CGO_ENABLED=0 \
        go build -tags {{_ui_tags}} -ldflags "-s -w -H windowsgui {{_identity}}" \
        -o build/{{app}}-ui.exe ./ui
    GOOS=windows GOARCH={{arch}} CGO_ENABLED=0 \
        go build -tags "{{_svc_tags}}" -ldflags "-s -w -H windowsgui {{_identity}}" \
        -o build/{{app}}-useragent.exe ./cmd/sofcat-useragent
    {{ if SIGN_CMD == "" { "true" } else { SIGN_CMD + " build/" + app + ".exe build/" + app + "-ui.exe build/" + app + "-useragent.exe" } }}

# Windows installer -> build/<app>-<pkg_version>-x86_64-setup.exe (embala's
# setup format; it builds on any OS, since the stub is prebuilt and embala
# only appends the payload). pkg_version is version as SemVer 2.0: the tag
# keeps its prerelease and git describe's suffix becomes build metadata, so
# v1.5.0-beta.1 -> 1.5.0-beta.1 and v1.5.0-3-gabc-dirty -> 1.5.0+3.gabc-dirty.
# installer/install.lua and uninstall.lua are
# rendered beside embala.toml with the same placeholders. With SIGN_CMD set, a
# [sign.windows] section is appended to the rendered embala.toml (the command
# as a TOML basic string, so \ and " in it are escaped, with embala's $f).
setup: build
    rm -rf build/setup
    mkdir -p build/setup
    cp build/{{app}}.exe build/{{app}}-ui.exe build/{{app}}-useragent.exe build/setup/
    for f in embala.toml install.lua uninstall.lua; do \
      sed -e "s/@VERSION@/{{pkg_version}}/" {{_subst}} installer/$f > build/setup/$f || exit 1; \
    done
    sign_cmd={{quote(SIGN_CMD)}}; if [ -n "$sign_cmd" ]; then \
      sign_cmd=${sign_cmd//\\/\\\\}; sign_cmd=${sign_cmd//\"/\\\"}; \
      printf '\n[sign.windows]\ncommand = "%s $f"\n' "$sign_cmd" >> build/setup/embala.toml; fi
    embala build --config build/setup/embala.toml --formats setup --out-dir build

# Release archive of the three Windows executables ->
# build/<app>-<version>-windows-amd64.zip (needs zip). amd64 only, like `setup`.
archive-windows: build
    rm -f build/{{app}}-{{version}}-windows-amd64.zip
    cd build && zip -X {{app}}-{{version}}-windows-amd64.zip {{app}}.exe {{app}}-ui.exe {{app}}-useragent.exe

# The oldest macOS the app and the cgo objects in it are built for.
macos_min := "13.0"
_app_bundle := "build/" + product + ".app"
# A brand icon.png, or the stock 1024px icon, becomes the app's icon.icns.
_icns_src := if icon_png == "" { "assets/sofcat-icon-1024.png" } else { icon_png }

_require-macos:
    @[ "$(uname)" = Darwin ] || { echo "this recipe runs only on macOS (Xcode command line tools: cgo, sips, iconutil, pkgbuild)" >&2; exit 1; }

# macOS binaries for the host architecture -> build/<app> (the CLI) and
# build/<app>-useragent (the per-user helper, pure Go) and
# build/<product>.app. The CLI needs cgo for the System keychain
# (tls_client_cert_store) and the UI for Wails, so both need the Xcode command
# line tools. Same tags and identity as `build`; no -H windowsgui. The icon.icns
# is made with sips and iconutil, which ship with macOS. With
# CODESIGN_IDENTITY set, the CLI and the helper are signed after their builds
# and the app as a bundle once it is complete, since the signature seals
# Info.plist and the icon too; the bundle's main executable is covered by
# that one signature.
build-macos: _require-macos ui-assets
    mkdir -p build
    {{ if write_roots == "" { "true" } else { "cp '" + write_roots + "' pkg/placement/brand-write-roots.yaml" } }}
    MACOSX_DEPLOYMENT_TARGET={{macos_min}} CGO_ENABLED=1 CGO_CFLAGS="-O2 -g -mmacosx-version-min={{macos_min}}" CGO_LDFLAGS="-mmacosx-version-min={{macos_min}}" \
        go build -tags "{{_svc_tags}}" -ldflags "-X github.com/hurricanehrndz/sofcat/pkg/version.appName={{app}} -X github.com/hurricanehrndz/sofcat/pkg/version.version={{version}} {{_identity}}" \
        -o build/{{app}} ./cmd/sofcat
    {{ if CODESIGN_IDENTITY == "" { "true" } else { _codesign + " --identifier " + identifier + ".cli build/" + app } }}
    CGO_ENABLED=0 go build -tags "{{_svc_tags}}" -ldflags "{{_identity}}" -o build/{{app}}-useragent ./cmd/sofcat-useragent
    {{ if CODESIGN_IDENTITY == "" { "true" } else { _codesign + " --identifier " + identifier + ".useragent build/" + app + "-useragent" } }}
    {{ if icon_png == "" { "true" } else { "cp '" + icon_png + "' ui/brand-icon.png" } }}
    rm -rf "{{_app_bundle}}" build/icon.iconset
    mkdir -p "{{_app_bundle}}/Contents/MacOS" "{{_app_bundle}}/Contents/Resources" build/icon.iconset
    MACOSX_DEPLOYMENT_TARGET={{macos_min}} CGO_ENABLED=1 CGO_CFLAGS="-O2 -g -mmacosx-version-min={{macos_min}}" CGO_LDFLAGS="-mmacosx-version-min={{macos_min}}" \
        go build -tags {{_ui_tags}} -ldflags "{{_identity}}" \
        -o "{{_app_bundle}}/Contents/MacOS/{{app}}-ui" ./ui
    for size in 16 32 128 256 512; do \
      sips -z $size $size "{{_icns_src}}" --out build/icon.iconset/icon_${size}x${size}.png >/dev/null && \
      sips -z $((size * 2)) $((size * 2)) "{{_icns_src}}" --out build/icon.iconset/icon_${size}x${size}@2x.png >/dev/null || exit 1; \
    done
    iconutil -c icns build/icon.iconset -o "{{_app_bundle}}/Contents/Resources/icon.icns"
    printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>' \
      '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
      '<plist version="1.0">' '<dict>' \
      '	<key>CFBundleIdentifier</key>' '	<string>{{identifier}}</string>' \
      '	<key>CFBundleName</key>' '	<string>{{product}}</string>' \
      '	<key>CFBundleDisplayName</key>' '	<string>{{product}}</string>' \
      '	<key>CFBundleExecutable</key>' '	<string>{{app}}-ui</string>' \
      '	<key>CFBundleIconFile</key>' '	<string>icon</string>' \
      '	<key>CFBundlePackageType</key>' '	<string>APPL</string>' \
      '	<key>CFBundleShortVersionString</key>' '	<string>{{version}}</string>' \
      '	<key>CFBundleVersion</key>' '	<string>{{version}}</string>' \
      '	<key>LSMinimumSystemVersion</key>' '	<string>{{macos_min}}</string>' \
      '	<key>NSHighResolutionCapable</key>' '	<true/>' \
      '</dict>' '</plist>' > "{{_app_bundle}}/Contents/Info.plist"
    plutil -lint "{{_app_bundle}}/Contents/Info.plist"
    {{ if CODESIGN_IDENTITY == "" { "true" } else { _codesign + " " + quote(_app_bundle) } }}

# Fail before any build when the signing variables do not go together: a
# signed pkg needs a signed app, and a notarized one both signatures and the
# notary key. Whether the key file exists is left to notarytool.
_sign-check:
    #!/usr/bin/env bash
    set -euo pipefail
    fail() { echo "pkg: $*" >&2; exit 1; }
    NOTARIZE={{quote(NOTARIZE)}}; CODESIGN_IDENTITY={{quote(CODESIGN_IDENTITY)}}; PRODUCTSIGN_IDENTITY={{quote(PRODUCTSIGN_IDENTITY)}}
    case $NOTARIZE in ''|1) ;; *) fail "NOTARIZE '$NOTARIZE' must be unset or 1" ;; esac
    [ -z "$PRODUCTSIGN_IDENTITY" ] || [ -n "$CODESIGN_IDENTITY" ] || fail "PRODUCTSIGN_IDENTITY needs CODESIGN_IDENTITY"
    [ "$NOTARIZE" != 1 ] || for v in CODESIGN_IDENTITY PRODUCTSIGN_IDENTITY; do [ -n "${!v}" ] || fail "NOTARIZE=1 needs $v"; done
    [ "$NOTARIZE" != 1 ] || [ -z {{quote(_notary_missing)}} ] || fail "NOTARIZE=1 needs {{_notary_missing}}"

# macOS installer -> build/<app>-<version>.pkg (unsigned without
# PRODUCTSIGN_IDENTITY): the app in
# /Applications, the CLI and the per-user helper in
# /Library/Application Support/<product>/bin, the launchd plist in
# /Library/LaunchDaemons and the helper's in /Library/LaunchAgents, all
# root:wheel. Each goes in its own component rooted at its install location,
# so the payload never lists
# /Library/Application Support, whose root:admin owner and mode Installer
# would otherwise reset. The plist is what
# `<app> -serviceinstall` writes for that layout (`-print-launchd-plist`, and
# `-print-useragent-plist` for the helper's), and
# the postinstall script bootstraps it, so the daemon starts with no config
# until config.yaml is written and the daemon kickstarted, as the Windows
# installer does. With PRODUCTSIGN_IDENTITY set, productbuild signs the
# product archive (the component pkgs stay unsigned; the product signature
# covers them), and with NOTARIZE=1 the pkg is then notarized in place.
pkg: _require-macos _sign-check build-macos
    #!/usr/bin/env bash
    set -euo pipefail
    work=build/pkg
    rm -rf "$work" && mkdir -p "$work/support/bin" "$work/daemon" "$work/agent" "$work/scripts"
    cp build/{{app}} build/{{app}}-useragent "$work/support/bin/"
    plist="$work/daemon/{{identifier}}.plist"
    build/{{app}} -print-launchd-plist > "$plist"
    agent_plist="$work/agent/{{identifier}}.useragent.plist"
    build/{{app}} -print-useragent-plist > "$agent_plist"
    plutil -lint "$plist" "$agent_plist"
    chmod 0644 "$plist" "$agent_plist" && chmod 0755 "$work/support" "$work/support/bin" "$work/support/bin/{{app}}" "$work/support/bin/{{app}}-useragent" "$work/daemon" "$work/agent"
    # The daemon logs into Data, root-only like the Windows data directory;
    # the service fixes its owner and mode again at every start. On an upgrade
    # the bootout returns before launchd has let go of the old daemon, and a
    # bootstrap that comes too soon fails with "Input/output error", so it is
    # retried for up to 10 s, as run-tapas-smoke.sh does.
    printf '%s\n' '#!/bin/sh' 'set -e' \
      'data="/Library/Application Support/{{product}}/Data"' \
      'plist="/Library/LaunchDaemons/{{identifier}}.plist"' \
      'mkdir -p "$data" && chown root:wheel "$data" && chmod 0700 "$data"' \
      '/bin/launchctl bootout system/{{identifier}} 2>/dev/null || true' \
      'tries=0' \
      'until /bin/launchctl bootstrap system "$plist"; do' \
      '  tries=$((tries + 1))' \
      '  [ "$tries" -lt 10 ] || exit 1' \
      '  sleep 1' \
      'done' > "$work/scripts/postinstall"
    chmod 0755 "$work/scripts/postinstall"
    # The app's component plist sets BundleIsRelocatable false, so Installer
    # always writes /Applications and never updates a copy of the bundle it
    # finds elsewhere, such as build/. The staging root stands for
    # /Applications, so it gets that directory's 0775.
    mkdir -m 0775 "$work/apps"
    ditto "{{_app_bundle}}" "$work/apps/{{product}}.app"
    pkgbuild --analyze --root "$work/apps" "$work/app-component.plist"
    /usr/libexec/PlistBuddy -c "Set :0:BundleIsRelocatable false" "$work/app-component.plist" 2>/dev/null || /usr/libexec/PlistBuddy -c "Add :0:BundleIsRelocatable bool false" "$work/app-component.plist"
    pkgbuild --root "$work/apps" --component-plist "$work/app-component.plist" --install-location /Applications \
      --identifier {{identifier}}.app --version {{version}} "$work/app.pkg"
    pkgbuild --root "$work/support" --install-location "/Library/Application Support/{{product}}" \
      --identifier {{identifier}}.cli --version {{version}} "$work/cli.pkg"
    # The helper's LaunchAgent: launchd loads it at each GUI login, and the
    # daemon starts it for users already signed in once it stores a plan.
    pkgbuild --root "$work/agent" --install-location /Library/LaunchAgents \
      --identifier {{identifier}}.useragent --version {{version}} "$work/agent.pkg"
    # Last, so its postinstall runs after the app, the CLI and the helper are
    # in place.
    pkgbuild --root "$work/daemon" --scripts "$work/scripts" --install-location /Library/LaunchDaemons \
      --identifier {{identifier}}.daemon --version {{version}} "$work/daemon.pkg"
    pkg=build/{{app}}-{{version}}.pkg
    productbuild --package "$work/app.pkg" --package "$work/cli.pkg" --package "$work/agent.pkg" --package "$work/daemon.pkg" \
      {{ if PRODUCTSIGN_IDENTITY == "" { "" } else { "--sign " + quote(PRODUCTSIGN_IDENTITY) } }} "$pkg"
    echo "Built $pkg"
    # The notary variables are passed on, so a `just VAR=... pkg` override
    # reaches the nested just as its environment.
    [ {{quote(NOTARIZE)}} != 1 ] || NOTARY_AUTH={{quote(NOTARY_AUTH)}} NOTARY_API_KEY_PATH={{quote(NOTARY_API_KEY_PATH)}} \
      NOTARY_API_KEY_ID={{quote(NOTARY_API_KEY_ID)}} NOTARY_API_ISSUER_ID={{quote(NOTARY_API_ISSUER_ID)}} {{just_executable()}} notarize "$pkg"

# Notarize a signed pkg in place: submit it to Apple's notary service and wait
# for the verdict, staple the ticket to it, then check the staple. Needs
# NOTARY_AUTH=api-key (the only mode) and an App Store Connect API key:
# NOTARY_API_KEY_PATH (the .p8 file), NOTARY_API_KEY_ID and
# NOTARY_API_ISSUER_ID. `pkg` runs this when NOTARIZE=1.
notarize pkg: _require-macos
    @[ -z {{quote(_notary_missing)}} ] || { echo "notarize: needs {{_notary_missing}}" >&2; exit 1; }
    xcrun notarytool submit {{quote(pkg)}} --key {{quote(NOTARY_API_KEY_PATH)}} --key-id {{quote(NOTARY_API_KEY_ID)}} --issuer {{quote(NOTARY_API_ISSUER_ID)}} --wait
    xcrun stapler staple {{quote(pkg)}}
    xcrun stapler validate {{quote(pkg)}}

# macOS release archive -> build/<app>-<version>-darwin-<arch>.tar.gz, for the
# host architecture: the CLI, the per-user helper and the app, as `pkg`
# installs them. It follows `pkg` because a tar.gz cannot carry a notarization
# ticket: with NOTARIZE=1 the pkg's notarization covers the code inside it, so
# the app's ticket is stapled to the bundle here (the two bare executables
# cannot hold one; Gatekeeper looks theirs up online). COPYFILE_DISABLE keeps
# AppleDouble `._` files out of the archive.
archive-macos: pkg
    {{ if NOTARIZE == "1" { "xcrun stapler staple " + quote(_app_bundle) } else { "true" } }}
    COPYFILE_DISABLE=1 tar -czf "build/{{app}}-{{version}}-darwin-$(go env GOARCH).tar.gz" -C build {{app}} {{app}}-useragent "{{product}}.app"

# Reject brand values that would break a template or collide with a stock
# install. SLUG, NAME and IDENTIFIER must all keep or all change the stock
# values: the service, pipe, ProgramData folder, policy key and installer
# identity derive from them. Two custom brands still need distinct values.
brand-check:
    #!/usr/bin/env bash
    set -euo pipefail
    export LC_ALL=C
    fail() { echo "brand: $*" >&2; exit 1; }
    app={{quote(app)}}; product={{quote(product)}}; identifier={{quote(identifier)}}
    slug_re='^[a-z][a-z0-9-]{1,31}$'
    name_re='^[A-Za-z0-9]([A-Za-z0-9 ._()-]{0,62}[A-Za-z0-9)])?$'
    id_re='^[A-Za-z0-9]+(\.[A-Za-z0-9-]+)+$'
    text_re='^[ -~]+$'
    [[ $app =~ $slug_re ]] || fail "SLUG '$app' must be 2-32 of a-z, 0-9 and -, starting with a letter"
    [[ $product =~ $name_re ]] || fail "NAME '$product' must be up to 64 of A-Z, a-z, 0-9, space and ._()- (it names a folder and a registry key)"
    [[ $identifier =~ $id_re ]] || fail "IDENTIFIER '$identifier' must be reverse-DNS, such as com.example.softwarecenter"
    for value in {{quote(publisher)}} {{quote(description)}} {{quote(homepage)}}; do
      [[ $value =~ $text_re && $value == "$(printf %s "$value" | tr -d '"|&\\'"'")" ]] ||
        fail "'$value' must be printable ASCII without \" \\ ' | or &"
    done
    [[ {{quote(homepage)}} == http://* || {{quote(homepage)}} == https://* ]] || fail "HOMEPAGE must be an http(s) URL"
    installers={{quote(installers)}}
    case $installers in ''|zip) ;; *) fail "INSTALLERS '$installers' must be unset or zip" ;; esac
    stock=0
    [ "$app" = sofcat ] && stock=$((stock + 1))
    [ "$(printf %s "$product" | tr A-Z a-z)" = sofcat ] && stock=$((stock + 1))
    [ "$identifier" = ca.hrndz.sofcat ] && stock=$((stock + 1))
    [ $stock = 0 ] || [ $stock = 3 ] || fail "SLUG, NAME and IDENTIFIER must all differ from stock SofCat (sofcat, SofCat, ca.hrndz.sofcat) or all match it"
    [ "$(head -c 4 {{quote(icon_ico)}} | od -An -tx1 | tr -d ' \n')" = 00000100 ] || fail "{{icon_ico}} is not a Windows .ico"
    [ -z {{quote(icon_png)}} ] || [ "$(head -c 8 {{quote(icon_png)}} | od -An -tx1 | tr -d ' \n')" = 89504e470d0a1a0a ] || fail "{{icon_png}} is not a PNG"

# Standalone makecatalogs for every admin platform (pure Go, no cgo)
# -> build/makecatalogs-<os>-<arch>[.exe]. With SIGN_CMD set, the two Windows
# ones are signed.
makecatalogs:
    mkdir -p build
    for os in linux darwin windows; do \
      for arch in amd64 arm64; do \
        ext=""; if [ "$os" = windows ]; then ext=".exe"; fi; \
        GOOS=$os GOARCH=$arch CGO_ENABLED=0 \
          go build -ldflags "-X github.com/hurricanehrndz/sofcat/pkg/version.version={{version}}" \
          -o build/makecatalogs-$os-$arch$ext ./cmd/makecatalogs || exit 1; \
      done; \
    done
    {{ if SIGN_CMD == "" { "true" } else { SIGN_CMD + " build/makecatalogs-windows-amd64.exe build/makecatalogs-windows-arm64.exe" } }}

# Release archives of those, each holding one binary named makecatalogs[.exe]
# -> build/makecatalogs-<os>-<arch>.tar.gz, or .zip for Windows (needs zip).
makecatalogs-archives: makecatalogs
    #!/usr/bin/env bash
    set -euo pipefail
    cd build
    rm -rf makecatalogs-archive
    for os in linux darwin windows; do
      for arch in amd64 arm64; do
        name=makecatalogs-$os-$arch
        mkdir -p makecatalogs-archive/$name
        if [ "$os" = windows ]; then
          cp $name.exe makecatalogs-archive/$name/makecatalogs.exe
          rm -f $name.zip && (cd makecatalogs-archive/$name && zip -X ../../$name.zip makecatalogs.exe)
        else
          cp $name makecatalogs-archive/$name/makecatalogs
          tar -czf $name.tar.gz -C makecatalogs-archive/$name makecatalogs
        fi
      done
    done

# Static file server for the manual-test loop -> build/manual-test-server
manual-test-server:
    mkdir -p build
    cd test/manual/server && go build -o ../../../build/manual-test-server .

# Manual-test assets under build/manual-test/ and VM scripts stamped with the
# server URL (auto-detected from the default route when base_url is empty).
bootstrap base_url="": build manual-test-server
    mkdir -p {{server_root}}/manifests {{server_root}}/catalogs {{server_root}}/packages {{vm_dir}}
    cp build/{{app}}.exe build/{{app}}-ui.exe build/{{app}}-useragent.exe {{server_root}}/
    cp examples/example_manifest.yaml {{server_root}}/manifests/
    cp examples/example_catalog.yaml {{server_root}}/catalogs/
    cp test/manual/fixtures/selfserve/manifests/*.yaml {{server_root}}/manifests/
    cp test/manual/fixtures/selfserve/catalogs/*.yaml {{server_root}}/catalogs/
    rm -rf {{server_root}}/packages/scripts
    cp -R test/manual/fixtures/selfserve/packages/scripts {{server_root}}/packages/scripts
    cp test/manual/bootstrap-vm.ps1 test/manual/bootstrap-vm-full.ps1 \
       test/manual/templates/run-sofcat-check.bat {{vm_dir}}/
    @base_url="{{base_url}}"; \
    if [ -z "$base_url" ]; then \
      case "$(uname)" in \
        Darwin) ip=$(ipconfig getifaddr "$(route -n get default 2>/dev/null | awk '/interface:/{print $2}' | head -n1)" 2>/dev/null || true) ;; \
        Linux) ip=$(hostname -I 2>/dev/null | awk '{print $1}') ;; \
        *) ip="" ;; \
      esac; \
      base_url="http://${ip:-localhost}:8080/"; \
    fi; \
    sed "s#@DEFAULT_BASE_URL@#$base_url#g" test/manual/templates/bootstrap-vm.bat > {{vm_dir}}/bootstrap-vm.bat; \
    sed "s#@DEFAULT_BASE_URL@#$base_url#g" test/manual/templates/bootstrap-vm-full.bat > {{vm_dir}}/bootstrap-vm-full.bat; \
    echo "$base_url" > {{vm_dir}}/base-url.txt; \
    echo "Using manual-test base URL: $base_url"; \
    echo "Prepared manual-test assets in {{server_root}}; VM scripts in {{vm_dir}}"; \
    echo "Run: ./build/manual-test-server -root {{server_root}} -addr :8080"

# bootstrap, then serve the assets on :8080.
bootstrap-run base_url="": (bootstrap base_url)
    ./build/manual-test-server -root {{server_root}} -addr :8080

tapas_fixtures := "test/manual/fixtures/tapas"
tapas_brand := tapas_fixtures / "brand"
tapas_app := shell(_read, tapas_brand / "brand.env", "SLUG", "")
tapas_product := shell(_read, tapas_brand / "brand.env", "NAME", "")
tapas_identifier := shell(_read, tapas_brand / "brand.env", "IDENTIFIER", "")
tapas_server_root := manual_test_dir / "tapas" / "server-root"
tapas_vm_dir := manual_test_dir / "tapas" / "vm"

# Zip-item manual-test assets for the Tapas Test fixture brand under
# build/manual-test/tapas/: its binaries and a repository whose zip items
# makecatalogs build packs from test/manual/fixtures/tapas/item-trees, plus
# the hand-written package-info files in packages-info/ (an exe item the
# zip-only brand must reject). The VM gate is test/manual/run-tapas-smoke.ps1.
bootstrap-tapas base_url="": manual-test-server _tapas-repo
    {{quote(just_executable())}} --justfile {{quote(justfile())}} brand={{tapas_brand}} build
    rm -rf {{tapas_vm_dir}}
    mkdir -p {{tapas_vm_dir}}
    cp build/{{tapas_app}}.exe build/{{tapas_app}}-ui.exe build/{{tapas_app}}-useragent.exe {{tapas_server_root}}/
    cp test/manual/bootstrap-vm.ps1 {{tapas_vm_dir}}/
    @base_url="{{base_url}}"; \
    if [ -z "$base_url" ]; then \
      case "$(uname)" in \
        Darwin) ip=$(ipconfig getifaddr "$(route -n get default 2>/dev/null | awk '/interface:/{print $2}' | head -n1)" 2>/dev/null || true) ;; \
        Linux) ip=$(hostname -I 2>/dev/null | awk '{print $1}') ;; \
        *) ip="" ;; \
      esac; \
      base_url="http://${ip:-localhost}:8080/"; \
    fi; \
    args='-Slug {{tapas_app}} -ProductName "{{tapas_product}}" -Manifest tapas_manifest -Catalogs tapas -InstallService -StartService'; \
    sed -e "s#@DEFAULT_BASE_URL@#$base_url#g" -e "s#-NoPause %\*#-NoPause $args %*#" \
      test/manual/templates/bootstrap-vm.bat > {{tapas_vm_dir}}/bootstrap-vm.bat; \
    echo "$base_url" > {{tapas_vm_dir}}/base-url.txt; \
    echo "Using manual-test base URL: $base_url"; \
    echo "Prepared Tapas assets in {{tapas_server_root}}; VM scripts in {{tapas_vm_dir}}"; \
    echo "Run: ./build/manual-test-server -root {{tapas_server_root}} -addr :8080"; \
    echo "VM:  bootstrap-vm.ps1 -BaseUrl $base_url $args -NoPause; then run-tapas-smoke.ps1"

tapas_mac_dir := manual_test_dir / "tapas" / "mac"

# The macOS twin of bootstrap-tapas, run on a Mac: the fixture brand's
# build-macos and pkg (build/<app>-<version>.pkg, installed by hand, not
# served), the same repository with the darwin CLI and a zip of the app beside
# it, and build/manual-test/tapas/mac/config.yaml pointing at base_url. The
# gate is test/manual/run-tapas-smoke.sh; see "macOS validation loop" in
# AGENTS.md.
bootstrap-tapas-macos base_url="http://127.0.0.1:8080/": _require-macos manual-test-server _tapas-repo
    {{quote(just_executable())}} --justfile {{quote(justfile())}} brand={{tapas_brand}} pkg
    cp build/{{tapas_app}} {{tapas_server_root}}/
    ditto -c -k --keepParent "build/{{tapas_product}}.app" "{{tapas_server_root}}/{{tapas_product}}.app.zip"
    rm -rf {{tapas_mac_dir}} && mkdir -p {{tapas_mac_dir}}
    printf '%s\n' 'url: {{base_url}}' 'url_packages: {{base_url}}' 'manifest: tapas_manifest' 'catalogs:' '  - tapas' \
      > {{tapas_mac_dir}}/config.yaml
    @echo "Prepared Tapas assets in {{tapas_server_root}}; config in {{tapas_mac_dir}}/config.yaml"
    @echo "Run: ./build/manual-test-server -root {{tapas_server_root}} -addr 127.0.0.1:8080"
    @echo "Then: sudo installer -pkg build/{{tapas_app}}-{{version}}.pkg -target / && sudo install -m 600 -o root -g wheel {{tapas_mac_dir}}/config.yaml '/Library/Application Support/{{tapas_product}}/config.yaml' && sudo launchctl kickstart -k system/{{tapas_identifier}} && sudo test/manual/run-tapas-smoke.sh"

# The Tapas fixture repository under build/manual-test/tapas/server-root,
# without binaries: what makecatalogs build makes of
# test/manual/fixtures/tapas, a windows and a darwin zip per placement of each
# item, plus the hand-written package-info files in packages-info/.
_tapas-repo: (_fixture-repo tapas_fixtures tapas_server_root "packages/tapas")

# A fixture directory built as a repository under root by makecatalogs build:
# placements.yaml, templates/, manifests/, the zip items packed from items/
# and item-trees/ into prefix, any hand-written package-info files and the
# condition scripts under conditions/, compiled and checked.
_fixture-repo fixtures root prefix:
    go build -ldflags "-X github.com/hurricanehrndz/sofcat/pkg/version.version={{version}}" \
        -o build/makecatalogs ./cmd/makecatalogs
    build/makecatalogs build {{quote(fixtures)}} -o {{quote(root)}} --location-prefix {{prefix}}

harness_fixtures := "test/manual/fixtures/harness"
harness_server_root := manual_test_dir / "harness" / "server-root"
harness_mac_dir := manual_test_dir / "harness" / "mac"

# The harness fixture for a Mac: the Tapas Test brand's pkg, the repository
# of a harness fixture directory (default the generic one) under
# build/manual-test/harness/server-root and
# build/manual-test/harness/mac/config.yaml pointing at base_url. The gate is
# test/manual/run-harness-smoke.sh; see "Harness validation loop" in
# AGENTS.md.
bootstrap-harness-macos fixture=harness_fixtures base_url="http://127.0.0.1:8080/": _require-macos manual-test-server (_harness-repo fixture)
    {{quote(just_executable())}} --justfile {{quote(justfile())}} brand={{tapas_brand}} pkg
    rm -rf {{harness_mac_dir}} && mkdir -p {{harness_mac_dir}}
    printf '%s\n' 'url: {{base_url}}' 'url_packages: {{base_url}}' 'manifest: harness_manifest' 'catalogs:' '  - harness' \
      > {{harness_mac_dir}}/config.yaml
    @echo "Prepared harness assets in {{harness_server_root}}; config in {{harness_mac_dir}}/config.yaml"
    @echo "Run: ./build/manual-test-server -root {{harness_server_root}} -addr 127.0.0.1:8080"
    @echo "Then: sudo installer -pkg build/{{tapas_app}}-{{version}}.pkg -target / && sudo install -m 600 -o root -g wheel {{harness_mac_dir}}/config.yaml '/Library/Application Support/{{tapas_product}}/config.yaml' && sudo test/manual/run-harness-smoke.sh {{quote(fixture)}}"
    @echo "Do not kickstart the daemon first: the smoke restarts it after saving the real harness config files."

# The harness fixture repository alone, which also builds on Linux.
_harness-repo fixture=harness_fixtures: (_fixture-repo fixture harness_server_root "packages/harness")

# Nupkg-item manual-test assets under build/manual-test/nupkg/: a repository of
# embala-built plain-style packages (test/manual/build-nupkg-repo.sh) and
# build/sofcat.exe. The VM gate is test/manual/run-nupkg-smoke.ps1, which
# writes its own config pointing at its -BaseUrl (default http://127.0.0.1:8080/).
bootstrap-nupkg: build manual-test-server
    test/manual/build-nupkg-repo.sh
    @echo "Prepared nupkg assets in build/manual-test/nupkg/server-root"
    @echo "Run: ./build/manual-test-server -root build/manual-test/nupkg/server-root -addr 127.0.0.1:8080"
    @echo "VM:  put build/sofcat.exe and test/manual/run-nupkg-smoke.ps1 in C:\\rig, then: rig ps test/manual/run-nupkg-smoke.ps1 (no arguments)"

# Guard that the tree keeps cross-compiling on Linux (CI-without-Windows goal).
check-xplat:
    GOOS=linux GOARCH=amd64 go build -o /dev/null ./...

# go vet for the deployment target (most of the tree is windows-tagged).
vet:
    GOOS=windows GOARCH=amd64 go vet ./...

# Go tests.
test:
    go test -cover -race ./...

# Go tests under the zip_only tag, the rule set INSTALLERS=zip builds.
test-zip-only:
    go test -cover -race -tags zip_only ./...

# Whole-tree lint gate. The second pass lints the *_windows.go files, which a
# Linux or macOS run skips.
lint:
    golangci-lint run ./...
    GOOS=windows golangci-lint run ./...

# Format the tree.
fmt *args:
    treefmt {{args}}

# Verify formatting without writing.
fmt-check:
    treefmt --fail-on-change --no-cache

# Remove build artifacts.
clean:
    rm -rf build/
