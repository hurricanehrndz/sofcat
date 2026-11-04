#!/usr/bin/env bash
# Host driver for the Tapas Test per-user authorization check on a Windows
# rig VM. It runs the steps of test/manual/run-tapas-authz.ps1 in order over
# two SSH sessions, the rig administrator's (`rig ps`) and the rig's standard
# user's (`rig ps --as std`), so no process is started under another user's
# credentials and no password passes through here. The guest script sees each
# caller's own token; the service sees the first as an administrator.
#
# Run after run-tapas-smoke.ps1 has passed on the same VM:
#   test/manual/run-tapas-authz.sh [-v VM]
# `rig` comes from the windows-test-rig skill (RIG overrides its path). The
# driver stops at the first failed step, prints every step's output, and ends
# with TAPAS AUTHZ PASSED.
set -euo pipefail

RIG=${RIG:-rig}
rig_args=()
if [[ ${1:-} == -v ]]; then
	rig_args=(-v "${2:?usage: run-tapas-authz.sh [-v VM]}")
	shift 2
fi
(($#)) && {
	echo "usage: run-tapas-authz.sh [-v VM]" >&2
	exit 2
}

here=$(cd "$(dirname "$0")" && pwd)
guest_script="$here/run-tapas-authz.ps1"
guest_path='C:\rig\run-tapas-authz.ps1'
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Colour only on a terminal, so captured output keeps plain PASSED lines.
if [[ -t 1 ]]; then
	cyan=$'\033[36m' red=$'\033[31m' green=$'\033[32m' reset=$'\033[0m'
else
	cyan='' red='' green='' reset=''
fi

# The standard user's SID, taken from its `select` output for verify-selector.
std_sid=""

# step ROLE NAME runs one guest step as the administrator (admin) or the
# standard user (std). `rig ps` takes a local .ps1 and re-splits its
# arguments at spaces, so each step gets a small wrapper file that calls the
# guest script with named parameters.
step() {
	local role=$1 name=$2 wrapper="$tmp/tapas-authz-$1-$2.ps1" out="$tmp/out" as=() selector=""
	[[ $role == std ]] && as=(--as std)
	[[ $name == verify-selector ]] && selector=" -Selector '$std_sid'"
	cat >"$wrapper" <<EOF
& '$guest_path' -Step '$name'$selector
exit \$LASTEXITCODE
EOF
	printf '%s== %s %s%s\n' "$cyan" "$role" "$name" "$reset"
	if ! "$RIG" "${rig_args[@]}" ps "${as[@]}" "$wrapper" 2>&1 | tr -d '\r' | tee "$out"; then
		printf '%sTAPAS AUTHZ FAILED at %s %s%s\n' "$red" "$role" "$name" "$reset" >&2
		exit 1
	fi
	if ! grep -q "^AUTHZ STEP $name PASSED$" "$out"; then
		printf '%sTAPAS AUTHZ FAILED: %s %s exited 0 without its PASSED line%s\n' "$red" "$role" "$name" "$reset" >&2
		exit 1
	fi
	if [[ $role == std && $name == select ]]; then
		std_sid=$(sed -n 's/^AUTHZ CALLER \(S-[0-9-]*\)$/\1/p' "$out" | head -n 1)
		[[ -n $std_sid ]] || {
			echo "TAPAS AUTHZ FAILED: no AUTHZ CALLER line in the standard user's select output" >&2
			exit 1
		}
	fi
}

"$RIG" "${rig_args[@]}" put "$guest_script"

step admin prepare
step admin select
step std remove-refused
step admin verify-unchanged
step admin remove
step std select
step admin verify-selector
step admin remove-other
step std select
step std remove-own
step admin verify-clean

printf '\n%sTAPAS AUTHZ PASSED%s\n' "$green" "$reset"
