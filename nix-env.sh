# Sourced by mise (the [env] table in mise.toml). On Linux with nix it loads
# flake.nix's dev shell, which holds the GTK 4/WebKitGTK 6.0 libraries the UI
# links against. Elsewhere it does nothing.
#
# `nix print-dev-env` takes seconds, so its output is cached under .nix-env/
# and rebuilt only when flake.nix or flake.lock changes. The profile link
# there is a GC root, so garbage collection keeps the shell's store paths.
[[ "$(uname -s)" == Linux ]] && command -v nix >/dev/null || return 0

flake_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cache="$flake_dir/.nix-env"
key="$(cat "$flake_dir/flake.nix" "$flake_dir/flake.lock" | sha256sum | cut -c1-16)"
if [[ ! -f "$cache/$key.sh" ]]; then
  rm -rf "$cache" && mkdir -p "$cache"
  nix print-dev-env --profile "$cache/profile" "$flake_dir" >"$cache/tmp.sh" &&
    mv "$cache/tmp.sh" "$cache/$key.sh" || return 0
fi

# The dev env is written for a build shell: it repoints SHELL and the temp
# dir variables at a fresh directory. Keep the caller's values instead.
saved_shell="$SHELL" saved_tmpdir="${TMPDIR-}"
source "$cache/$key.sh"
rmdir "$NIX_BUILD_TOP" 2>/dev/null
unset NIX_BUILD_TOP TMP TEMP TEMPDIR TMPDIR
export SHELL="$saved_shell"
[[ -n "$saved_tmpdir" ]] && export TMPDIR="$saved_tmpdir"
return 0
