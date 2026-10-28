# sofcat task runner. All build artifacts go to build/.
# Tools come from mise.toml (`mise install`); `just setup` wires the git hook.
set shell := ["bash", "-cu"]

# Rebranding: `just brand=branding/acme build` (or msi) takes the values below
# from <brand>/brand.env, a path relative to this file, instead of the stock
# SofCat ones; see examples/brand/README.md. pkg/identity defaults to the same
# stock values for builds that bypass just.
brand := ""
_brand_env := if brand == "" { "" } else { brand / "brand.env" }
_read := 'unset SLUG NAME PUBLISHER IDENTIFIER DESCRIPTION HOMEPAGE; [ -z "$1" ] || . "$1"; printf %s "${!2:-$3}"'
app := shell(_read, _brand_env, "SLUG", "sofcat")
product := shell(_read, _brand_env, "NAME", "SofCat")
publisher := shell(_read, _brand_env, "PUBLISHER", "Carlos Hernandez")
identifier := shell(_read, _brand_env, "IDENTIFIER", "ca.hrndz.sofcat")
description := shell(_read, _brand_env, "DESCRIPTION", "A catalog for anything you can put on disk or run")
homepage := shell(_read, _brand_env, "HOMEPAGE", "https://github.com/hurricanehrndz/sofcat")
_brand_ico := if brand == "" { "" } else { brand / "icon.ico" }
_brand_png := if brand == "" { "" } else { brand / "icon.png" }
icon_ico := if path_exists(_brand_ico) == "true" { _brand_ico } else { "assets/sofcat.ico" }
icon_png := if path_exists(_brand_png) == "true" { _brand_png } else { "" }
# sed arguments filling the @PLACEHOLDERS@ in installer/embala.toml and
# */winres/winres.json. brand-check keeps the values free of ' | & \ and ".
_subst := "-e 's|@APP@|" + app + "|g' -e 's|@PRODUCT@|" + product + "|g' -e 's|@PUBLISHER@|" + publisher + "|g' -e 's|@IDENTIFIER@|" + identifier + "|g' -e 's|@DESCRIPTION@|" + description + "|g' -e 's|@HOMEPAGE@|" + homepage + "|g'"
_identity := "-X github.com/hurricanehrndz/sofcat/pkg/identity.Slug=" + app + " -X 'github.com/hurricanehrndz/sofcat/pkg/identity.Name=" + product + "'"
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
msi_version := replace_regex(replace_regex(version, "^v", ""), "[-+].*$", "")
manual_test_dir := "build/manual-test"
server_root := manual_test_dir / "server-root"
vm_dir := manual_test_dir / "vm"

# List recipes.
default:
    @just --list

# One-time contributor setup: frontend deps and the prek pre-commit hook
# (prek.toml). --force replaces a hook left by an older setup.
setup: ui-install
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
    cd ui && go run github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.34 generate bindings -clean -ts -noevents -d ../build/ui-bindings-check .
    diff -ru ui/frontend/bindings build/ui-bindings-check

# Type check plus committed-binding verification.
ui-lint: ui-type ui-bindings-check

# Windows resources (rsrc_windows_<arch>.syso, gitignored) that go build links
# into each executable: icon, VERSIONINFO (company, product, and file and
# product version from `version`) and an asInvoker application manifest, all
# from <pkg>/winres/winres.json. Unsigned binaries without these look anonymous
# to antivirus ML heuristics (docs/code-signing.md). The UI manifest declares
# what Wails' own template does: per-monitor-v2 DPI and common controls v6.
# Each template is rendered with the brand values beside a copy of the icon
# under build/winres/<pkg>/.
winres arch="amd64": brand-check
    rm -rf build/winres
    for dir in cmd/sofcat ui; do \
      out=build/winres/$dir && mkdir -p $out && cp "{{icon_ico}}" $out/icon.ico && \
      sed {{_subst}} $dir/winres/winres.json > $out/winres.json && \
      go run github.com/tc-hib/go-winres@v0.3.3 make --arch {{arch}} --in $out/winres.json \
        --file-version "{{version}}" --product-version "{{version}}" --out $dir/rsrc || exit 1; \
    done

# Windows binaries (pure Go, no cgo) -> build/<app>.exe and build/<app>-ui.exe.
# A brand icon.png is staged as ui/brand-icon.png (gitignored) for the brand tag.
build arch="amd64": ui-assets (winres arch)
    mkdir -p build
    GOOS=windows GOARCH={{arch}} CGO_ENABLED=0 \
        go build -ldflags "-X github.com/hurricanehrndz/sofcat/pkg/version.appName={{app}} -X github.com/hurricanehrndz/sofcat/pkg/version.version={{version}} {{_identity}}" \
        -o build/{{app}}.exe ./cmd/sofcat
    {{ if icon_png == "" { "true" } else { "cp '" + icon_png + "' ui/brand-icon.png" } }}
    GOOS=windows GOARCH={{arch}} CGO_ENABLED=0 \
        go build -tags {{ if icon_png == "" { "production" } else { "production,brand" } }} -ldflags "-H windowsgui {{_identity}}" \
        -o build/{{app}}-ui.exe ./ui

# Windows installer -> build/<app>-<msi_version>-x86_64.msi (embala, no WiX).
# msi_version is the numeric part of version: v1.5.0-3-gabc-dirty -> 1.5.0.
msi: build
    rm -rf build/msi
    mkdir -p build/msi
    cp build/{{app}}.exe build/{{app}}-ui.exe build/msi/
    sed -e "s/@VERSION@/{{msi_version}}/" {{_subst}} installer/embala.toml > build/msi/embala.toml
    embala build --config build/msi/embala.toml --formats msi --out-dir build

# Reject brand values that would break a template or collide with a stock
# install. SLUG, NAME and IDENTIFIER must all keep or all change the stock
# values: the service, pipe, ProgramData folder, policy key and MSI upgrade
# code derive from them. Two custom brands still need distinct values.
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
    stock=0
    [ "$app" = sofcat ] && stock=$((stock + 1))
    [ "$(printf %s "$product" | tr A-Z a-z)" = sofcat ] && stock=$((stock + 1))
    [ "$identifier" = ca.hrndz.sofcat ] && stock=$((stock + 1))
    [ $stock = 0 ] || [ $stock = 3 ] || fail "SLUG, NAME and IDENTIFIER must all differ from stock SofCat (sofcat, SofCat, ca.hrndz.sofcat) or all match it"
    [ "$(head -c 4 {{quote(icon_ico)}} | od -An -tx1 | tr -d ' \n')" = 00000100 ] || fail "{{icon_ico}} is not a Windows .ico"
    [ -z {{quote(icon_png)}} ] || [ "$(head -c 8 {{quote(icon_png)}} | od -An -tx1 | tr -d ' \n')" = 89504e470d0a1a0a ] || fail "{{icon_png}} is not a PNG"

# Standalone makecatalogs for every admin platform (pure Go, no cgo)
# -> build/makecatalogs-<os>-<arch>[.exe]
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

# Static file server for the manual-test loop -> build/manual-test-server
manual-test-server:
    mkdir -p build
    cd test/manual/server && go build -o ../../../build/manual-test-server .

# Manual-test assets under build/manual-test/ and VM scripts stamped with the
# server URL (auto-detected from the default route when base_url is empty).
bootstrap base_url="": build manual-test-server
    mkdir -p {{server_root}}/manifests {{server_root}}/catalogs {{server_root}}/packages {{vm_dir}}
    cp build/{{app}}.exe build/{{app}}-ui.exe {{server_root}}/
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

# Guard that the tree keeps cross-compiling on Linux (CI-without-Windows goal).
check-xplat:
    GOOS=linux GOARCH=amd64 go build -o /dev/null ./...

# go vet for the deployment target (most of the tree is windows-tagged).
vet:
    GOOS=windows GOARCH=amd64 go vet ./...

# Go tests.
test:
    go test -cover -race ./...

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
