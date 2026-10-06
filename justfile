# sofcat task runner. All build artifacts go to build/.
# Tools come from mise.toml (`mise install`); `just setup` wires the git hook.
set shell := ["bash", "-cu"]

app := "sofcat"
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
msi_version := replace_regex(replace_regex(version, "^v", ""), "[-+].*$", "")
manual_test_dir := "build/manual-test"
server_root := manual_test_dir / "server-root"
vm_dir := manual_test_dir / "vm"

# List recipes.
default:
    @just --list

# One-time contributor setup: frontend deps and the pre-commit hook.
setup: ui-install
    printf '#!/bin/sh\nexec just pre-commit\n' > .git/hooks/pre-commit
    chmod +x .git/hooks/pre-commit

# What the pre-commit hook runs.
pre-commit: fmt-check lint

# Frontend dependencies.
ui-install:
    npm ci --prefix ui/frontend

# Frontend type check.
ui-type: ui-install
    npm run check --prefix ui/frontend

# Frontend tests.
ui-test: ui-install
    npm test --prefix ui/frontend

# Production frontend assets.
ui-assets: ui-install
    npm run build --prefix ui/frontend
    @if grep -rq SOFCAT_VITE_MOCK_ONLY ui/frontend/dist; then \
      echo "Dev mock leaked into the production bundle" && exit 1; \
      else echo "Production bundle is mock-free"; fi

# Verify committed Wails bindings.
ui-bindings-check:
    rm -rf build/ui-bindings-check
    mkdir -p build
    cd ui && go run github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha2.117 generate bindings -clean -ts -noevents -d ../build/ui-bindings-check .
    diff -ru ui/frontend/bindings build/ui-bindings-check

# Type check plus committed-binding verification.
ui-lint: ui-type ui-bindings-check

# Windows binaries (pure Go, no cgo) -> build/sofcat.exe and build/sofcat-ui.exe
build arch="amd64": ui-assets
    mkdir -p build
    GOOS=windows GOARCH={{arch}} CGO_ENABLED=0 \
        go build -ldflags "-X github.com/hurricanehrndz/sofcat/pkg/version.appName={{app}} -X github.com/hurricanehrndz/sofcat/pkg/version.version={{version}}" \
        -o build/{{app}}.exe ./cmd/sofcat
    GOOS=windows GOARCH={{arch}} CGO_ENABLED=0 \
        go build -tags production -ldflags "-H windowsgui" \
        -o build/sofcat-ui.exe ./ui

# Windows installer -> build/sofcat-<msi_version>-x86_64.msi (embala, no WiX).
# msi_version is the numeric part of version: v1.5.0-3-gabc-dirty -> 1.5.0.
msi: build
    rm -rf build/msi
    mkdir -p build/msi
    cp build/{{app}}.exe build/sofcat-ui.exe build/msi/
    sed "s/@VERSION@/{{msi_version}}/" installer/embala.toml > build/msi/embala.toml
    embala build --config build/msi/embala.toml --formats msi --out-dir build

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
    cd utils/manual-test/server && go build -o ../../../build/manual-test-server .

# Manual-test assets under build/manual-test/ and VM scripts stamped with the
# server URL (auto-detected from the default route when base_url is empty).
bootstrap base_url="": build manual-test-server
    mkdir -p {{server_root}}/manifests {{server_root}}/catalogs {{server_root}}/packages {{vm_dir}}
    cp build/{{app}}.exe build/sofcat-ui.exe {{server_root}}/
    cp examples/example_manifest.yaml {{server_root}}/manifests/
    cp examples/example_catalog.yaml {{server_root}}/catalogs/
    cp utils/manual-test/fixtures/selfserve/manifests/*.yaml {{server_root}}/manifests/
    cp utils/manual-test/fixtures/selfserve/catalogs/*.yaml {{server_root}}/catalogs/
    rm -rf {{server_root}}/packages/scripts
    cp -R utils/manual-test/fixtures/selfserve/packages/scripts {{server_root}}/packages/scripts
    cp utils/manual-test/bootstrap-vm.ps1 utils/manual-test/bootstrap-vm-full.ps1 \
       utils/manual-test/templates/run-sofcat-check.bat \
       utils/manual-test/run-release-integration.bat {{vm_dir}}/
    @base_url="{{base_url}}"; \
    if [ -z "$base_url" ]; then \
      case "$(uname)" in \
        Darwin) ip=$(ipconfig getifaddr "$(route -n get default 2>/dev/null | awk '/interface:/{print $2}' | head -n1)" 2>/dev/null || true) ;; \
        Linux) ip=$(hostname -I 2>/dev/null | awk '{print $1}') ;; \
        *) ip="" ;; \
      esac; \
      base_url="http://${ip:-localhost}:8080/"; \
    fi; \
    sed "s#@DEFAULT_BASE_URL@#$base_url#g" utils/manual-test/templates/bootstrap-vm.bat > {{vm_dir}}/bootstrap-vm.bat; \
    sed "s#@DEFAULT_BASE_URL@#$base_url#g" utils/manual-test/templates/bootstrap-vm-full.bat > {{vm_dir}}/bootstrap-vm-full.bat; \
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

# Incremental lint gate (only issues new vs {{rev}}).
lint rev="main":
    golangci-lint run --new-from-merge-base={{rev}} ./...

# Whole-tree lint audit (non-gating; drives Workstream B cleanup).
lint-all:
    golangci-lint run ./...

# Format the tree.
fmt *args:
    treefmt {{args}}

# Verify formatting without writing.
fmt-check:
    treefmt --fail-on-change --no-cache

# Remove build artifacts.
clean:
    rm -rf build/
