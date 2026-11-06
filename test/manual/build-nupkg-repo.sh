#!/usr/bin/env bash
# Lay out the nupkg smoke repository for test/manual/run-nupkg-smoke.ps1 under
# build/manual-test/nupkg/server-root (`just bootstrap-nupkg` runs this).
#
# embala builds three plain-style packages, which choco install refuses:
#   sofcat-nupkg-smoke 1.0.0 and 2.0.0   scripts = "generate", a text payload
#   sofcat-nupkg-bad 1.0.0               fixtures/nupkg/bad-install.ps1, which
#                                        calls an unknown command
# and the catalog items point at them by hash, with package_id and no check
# block, so sofcat checks them against its kept copy. makecatalogs compiles
# the catalog and -check fails on any nupkg item it would refuse.
#
# Needs embala 0.10.0 or later on PATH (mise install) and go.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

fixtures=test/manual/fixtures/nupkg
out=build/manual-test/nupkg
repo=$out/server-root
work=$out/work

have=$(embala --version | awk '{print $2}')
if [[ $(printf '%s\n' 0.10.0 "$have" | sort -V | head -n1) != 0.10.0 ]]; then
	echo "embala $have is older than 0.10.0; run mise install" >&2
	exit 1
fi

rm -rf "$out"
mkdir -p "$work" "$repo"/{manifests,packages-info,packages/nupkg}
cp "$fixtures"/manifests/*.yaml "$repo/manifests/"

# pkg NAME VERSION DISPLAY-NAME SCRIPTS: build NAME-VERSION.nupkg into
# packages/nupkg with a readme.txt payload that names the version.
pkg() {
	local name=$1 version=$2 display=$3 scripts=$4 dir=$work/$1-$2
	mkdir -p "$dir"
	echo "$name $version" >"$dir/readme.txt"
	cp "$fixtures/bad-install.ps1" "$dir/"
	cat >"$dir/embala.toml" <<EOF
[package]
name = "$name"
display-name = "$display"
version = "$version"
identifier = "ca.hrndz.sofcat.$name"
publisher = "Carlos Hernandez"
description = "sofcat nupkg smoke test package"

[nupkg]
style = "plain"
scripts = $scripts
files = [{ src = "readme.txt", dest = "readme.txt" }]
EOF
	embala build --config "$dir/embala.toml" --formats nupkg --out-dir "$repo/packages/nupkg"
}

# item ITEM NAME VERSION DISPLAY-NAME: the packages-info file for one package.
item() {
	local item=$1 name=$2 version=$3 display=$4 location hash
	location=packages/nupkg/$name-$version.nupkg
	hash=$(sha256sum "$repo/$location" | cut -d' ' -f1)
	cat >"$repo/packages-info/$item.yaml" <<EOF
item_name: $item
display_name: $display
catalog: nupkg
version: $version
installer:
  type: nupkg
  location: $location
  hash: $hash
  package_id: $name
uninstaller:
  type: nupkg
  location: $location
  hash: $hash
EOF
}

pkg sofcat-nupkg-smoke 1.0.0 "SofCat Nupkg Smoke" '"generate"'
pkg sofcat-nupkg-smoke 2.0.0 "SofCat Nupkg Smoke" '"generate"'
pkg sofcat-nupkg-bad 1.0.0 "SofCat Nupkg Bad" '{ install = "bad-install.ps1" }'
item nupkg-smoke-v1 sofcat-nupkg-smoke 1.0.0 "SofCat Nupkg Smoke"
item nupkg-smoke-v2 sofcat-nupkg-smoke 2.0.0 "SofCat Nupkg Smoke"
item nupkg-bad sofcat-nupkg-bad 1.0.0 "SofCat Nupkg Bad"

go run ./cmd/makecatalogs "$repo"
go run ./cmd/makecatalogs -check "$repo"
