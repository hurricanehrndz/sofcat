#!/bin/bash
# Harness config end-to-end smoke test for the Tapas Test fixture brand on
# macOS: the machine-wide config files of Claude Code and Codex, written as
# placement files from templates named by template_file.
#
# It restarts the running Tapas Test launchd daemon against the repository
# `just bootstrap-harness-macos` lays out from a harness fixture directory
# (default test/manual/fixtures/harness) and, for each case under the
# fixture's cases/, asserts on the files the daemon's startup run writes:
# their content against cases/<case>/expected/ (a .json file parsed, any
# other byte for byte), owner root:wheel and mode 0644, and every file of the
# placed item trees against the fixture's copy by hash. It then switches the
# daemon to harness_empty_manifest and asserts every file is gone.
#
# WARNING: the expected files are mirrored by absolute path, and those are
# the real harness config paths, such as /etc/codex/config.toml. The smoke
# saves every file already at one of them under the data directory and its
# exit trap puts them back, pass or fail, and removes what the smoke wrote:
# the fixture's condition scripts, the facts file and the config change. The
# trap also leaves the daemon stopped, because a later run would rewrite or
# remove the restored files. Remove the fixture brand before the Mac next
# restarts (AGENTS.md, "Harness validation loop").
#
# Run as root after installing the fixture brand's .pkg and writing the config
# `just bootstrap-harness-macos` made. Do not kickstart the daemon first: a run
# before the smoke has saved the target files could overwrite them.
# The smoke restarts the daemon itself:
#   sudo test/manual/run-harness-smoke.sh [fixture-dir]
#
# Exits non-zero on the first failed assertion; prints each step and
# HARNESS SMOKE PASSED at the end. Bash 3.2 compatible (the macOS /bin/bash).

set -u

FIXTURE="${1:-$(dirname "$0")/fixtures/harness}"
SLUG="${SLUG:-tapas-test}"
PRODUCT="${PRODUCT:-Tapas Test}"
LABEL="${LABEL:-ca.hrndz.tapas-test}"
SUPPORT="/Library/Application Support/$PRODUCT"
SOFCAT="${SOFCAT:-$SUPPORT/bin/$SLUG}"
CONFIG="${CONFIG:-$SUPPORT/config.yaml}"
DATA_DIR="${DATA_DIR:-$SUPPORT/Data}"
PLIST="/Library/LaunchDaemons/$LABEL.plist"
SOCKET="/var/run/$SLUG/$SLUG-service.sock"
TIMEOUT_SEC="${TIMEOUT_SEC:-120}"

RECEIPT_DIR="$DATA_DIR/receipts"
CONDITIONS_DIR="$DATA_DIR/conditions"
# The generic fixture's fact script prints this file; the smoke fills it from
# cases/<case>/facts.json. Root-only, as the data directory is.
FACTS_FILE="$DATA_DIR/harness-facts.json"
# The files found at the target paths and the config, until the trap puts
# them back. In the data directory so a restart of the Mac cannot lose them.
SAVE_DIR="$DATA_DIR/harness-smoke-saved"

step_num=0

step() {
	step_num=$((step_num + 1))
	printf '\033[36m[%d] %s\033[0m\n' "$step_num" "$*"
}

fail() {
	printf '\033[31mASSERTION FAILED: %s\033[0m\n' "$*" >&2
	exit 1
}

pass() {
	printf '\033[32m    %s\033[0m\n' "$*"
}

wait_for() {
	local desc="$1"
	shift
	local deadline=$(($(date +%s) + TIMEOUT_SEC))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if "$@"; then return 0; fi
		sleep 3
	done
	fail "timed out after ${TIMEOUT_SEC}s waiting for: $desc"
}

sha() { shasum -a 256 "$1" | awk '{print $1}'; }

# targets prints the absolute path of every file a case expects, once each:
# cases/<case>/expected/etc/codex/config.toml is /etc/codex/config.toml.
targets() {
	local expected
	for expected in "$FIXTURE"/cases/*/expected; do
		(cd "$expected" && find . -type f | sed 's/^\.//')
	done | sort -u
}

# same_content EXPECTED ACTUAL compares a .json file as parsed JSON and any
# other file byte for byte.
same_content() {
	local want got
	case "$1" in
	*.json)
		want=$(jq -S . "$1" 2>/dev/null) && got=$(jq -S . "$2" 2>/dev/null) || return 1
		[ "$want" = "$got" ]
		;;
	*) cmp -s "$1" "$2" ;;
	esac
}

# case_written EXPECTED_DIR says whether every expected file is at its target
# path with the expected content.
case_written() {
	local rel
	while IFS= read -r rel; do
		[ -f "$rel" ] || return 1
		same_content "$1$rel" "$rel" || return 1
	done <<EOF
$(cd "$1" && find . -type f | sed 's/^\.//')
EOF
}

all_gone() {
	local target
	while IFS= read -r target; do
		[ ! -e "$target" ] || return 1
	done <<EOF
$TARGETS
EOF
}

running() { launchctl print "system/$LABEL" 2>/dev/null | grep -q 'state = running'; }
bootstrapped() { launchctl bootstrap system "$PLIST" 2>/dev/null; }
answers() { [ -S "$SOCKET" ] && "$SOFCAT" -c "$CONFIG" -S GetServiceInfo >/dev/null 2>&1; }
unloaded() { ! launchctl print "system/$LABEL" >/dev/null 2>&1; }

# stop_daemon boots the daemon out, so no run starts while the smoke changes
# its facts, its config or the target files.
stop_daemon() {
	launchctl bootout "system/$LABEL" 2>/dev/null
	wait_for "system/$LABEL to unload" unloaded
}

# start_daemon bootstraps the daemon again. Its startup run is the run under
# test: it reads the config, runs the condition scripts and writes or removes
# every placement file.
start_daemon() {
	wait_for "launchctl bootstrap system $PLIST" bootstrapped
	wait_for "system/$LABEL to be running" running
	wait_for "the daemon to answer on $SOCKET" answers
}

# cleanup is the exit trap: the daemon stopped, the target files as they were
# before the smoke, and nothing of the smoke's left. It runs on every exit
# once every target file is saved, and must not call fail.
cleanup() {
	local code=$? restored=yes target dir i=0
	trap - EXIT
	# A second signal must not stop the restore halfway.
	trap '' INT TERM HUP
	printf '\033[36mRestoring the target files\033[0m\n'
	launchctl bootout "system/$LABEL" 2>/dev/null
	while ! unloaded && [ "$i" -lt 30 ]; do
		sleep 1
		i=$((i + 1))
	done
	while IFS= read -r target; do
		rm -f "$target"
		if [ -f "$SAVE_DIR/files$target" ]; then
			if cp -p "$SAVE_DIR/files$target" "$target"; then
				printf '    restored %s\n' "$target"
			else
				restored=no
			fi
		fi
		# A directory the daemon created for the file goes with it.
		dir=$(dirname "$target")
		if grep -qxF "$dir" "$SAVE_DIR/created-dirs" 2>/dev/null; then
			rmdir "$dir" 2>/dev/null
		fi
	done <<EOF
$TARGETS
EOF
	if [ -f "$SAVE_DIR/config.yaml" ]; then
		cat "$SAVE_DIR/config.yaml" >"$CONFIG" || restored=no
	fi
	while IFS= read -r target; do
		[ -z "$target" ] || rm -f "$CONDITIONS_DIR/$target"
	done <"$SAVE_DIR/condition-scripts"
	rm -f "$FACTS_FILE"
	if [ "$restored" = yes ]; then
		rm -rf "$SAVE_DIR"
	else
		code=1
		printf '\033[31mThe saved files stay in %s; check the target files against them, then remove it.\033[0m\n' "$SAVE_DIR" >&2
	fi
	printf '    system/%s is stopped; remove the fixture brand before it runs again\n' "$LABEL"
	exit "$code"
}

# --- Preconditions --------------------------------------------------------------

[ "$(id -u)" = 0 ] || fail "run as root: sudo $0"
for tool in jq shasum cmp; do
	command -v "$tool" >/dev/null || fail "$tool is missing"
done
[ -x "$SOFCAT" ] || fail "$SOFCAT is missing; install the fixture brand's .pkg first"
[ -f "$CONFIG" ] || fail "$CONFIG is missing; write the harness config first"
[ -f "$PLIST" ] || fail "$PLIST is missing; install the fixture brand's .pkg first"
grep -qx 'manifest: harness_manifest' "$CONFIG" || fail "$CONFIG does not set manifest: harness_manifest; write the harness config first"
[ -d "$FIXTURE/cases" ] || fail "$FIXTURE has no cases directory; pass a harness fixture directory"
FIXTURE=$(cd "$FIXTURE" && pwd)
TARGETS=$(targets)
[ -n "$TARGETS" ] || fail "$FIXTURE/cases holds no expected file"
[ ! -e "$SAVE_DIR" ] || fail "$SAVE_DIR exists: an earlier run did not restore the target files. Put its files/ back by hand, then remove it"
for script in "$FIXTURE"/conditions/*; do
	[ -f "$script" ] || fail "$FIXTURE/conditions holds no fact script"
	[ ! -e "$CONDITIONS_DIR/$(basename "$script")" ] || fail "$CONDITIONS_DIR/$(basename "$script") exists before the smoke wrote it"
done

# --- Save the target files ------------------------------------------------------

step "Stop the daemon and save the files at the target paths"
stop_daemon
mkdir -p "$SAVE_DIR/files" || fail "could not create $SAVE_DIR"
: >"$SAVE_DIR/condition-scripts"
: >"$SAVE_DIR/created-dirs"
# Until every file is saved nothing has changed, so a failure here only drops
# the partial copies. cleanup removes target files, so it must not run yet.
trap 'rm -rf "$SAVE_DIR"' EXIT
trap 'exit 1' INT TERM HUP
cp -p "$CONFIG" "$SAVE_DIR/config.yaml" || fail "could not save $CONFIG"
saved=0
while IFS= read -r target; do
	dir=$(dirname "$target")
	[ -d "$dir" ] || printf '%s\n' "$dir" >>"$SAVE_DIR/created-dirs"
	[ ! -L "$target" ] || fail "$target is a symlink, which the smoke could not put back as it was"
	[ -e "$target" ] || continue
	[ -f "$target" ] || fail "$target exists and is not a regular file"
	mkdir -p "$SAVE_DIR/files$dir" || fail "could not create $SAVE_DIR/files$dir"
	cp -p "$target" "$SAVE_DIR/files$target" || fail "could not save $target"
	saved=$((saved + 1))
done <<EOF
$TARGETS
EOF
trap cleanup EXIT
pass "$saved of $(printf '%s\n' "$TARGETS" | wc -l | tr -d ' ') target files existed; saved under $SAVE_DIR"

step "Install the fixture's condition scripts, root-owned and executable"
mkdir -p "$CONDITIONS_DIR"
for script in "$FIXTURE"/conditions/*; do
	name=$(basename "$script")
	printf '%s\n' "$name" >>"$SAVE_DIR/condition-scripts"
	cp "$script" "$CONDITIONS_DIR/$name" || fail "could not install $name"
	chown root:wheel "$CONDITIONS_DIR/$name"
	chmod 0755 "$CONDITIONS_DIR/$name"
done
pass "$(tr '\n' ' ' <"$SAVE_DIR/condition-scripts")in $CONDITIONS_DIR"

# --- The cases --------------------------------------------------------------------

for case_dir in "$FIXTURE"/cases/*/; do
	case_dir=${case_dir%/}
	case_name=$(basename "$case_dir")
	expected="$case_dir/expected"
	[ -d "$expected" ] || fail "$case_dir has no expected directory"

	step "Case $case_name: a run writes the expected files"
	stop_daemon
	# A case without facts.json relies on the fixture's own fact script.
	rm -f "$FACTS_FILE"
	if [ -f "$case_dir/facts.json" ]; then
		install -m 600 -o root -g wheel "$case_dir/facts.json" "$FACTS_FILE" || fail "could not write $FACTS_FILE"
	fi
	# With no file left, one that matches was written by this run.
	while IFS= read -r target; do
		rm -f "$target"
	done <<EOF
$TARGETS
EOF
	start_daemon
	deadline=$(($(date +%s) + TIMEOUT_SEC))
	until case_written "$expected" || [ "$(date +%s)" -ge "$deadline" ]; do
		sleep 3
	done
	while IFS= read -r target; do
		[ -f "$target" ] || fail "$target was not written within ${TIMEOUT_SEC}s; see $DATA_DIR/sofcat.log"
		same_content "$expected$target" "$target" || fail "$target differs from $expected$target:
$(diff "$expected$target" "$target")"
		perm=$(stat -f '%Su:%Sg %Lp' "$target")
		[ "$perm" = "root:wheel 644" ] || fail "$target is $perm, want root:wheel 644"
		pass "$target matches, $perm"
	done <<EOF
$(cd "$expected" && find . -type f | sed 's/^\.//')
EOF

	# rendered/<item>/<placement>/darwin is the tree as packed; the receipt
	# says where the daemon placed it. The Codex model catalog is one of them.
	for tree in "$FIXTURE"/rendered/*/*/darwin; do
		placement=$(basename "$(dirname "$tree")")
		item=$(basename "$(dirname "$(dirname "$tree")")")
		rc="$RECEIPT_DIR/$placement/$item.json"
		[ -f "$rc" ] || fail "receipt $rc is missing"
		placed=$(jq -r .path "$rc")
		while IFS= read -r rel; do
			[ -f "$placed/$rel" ] || fail "$placed/$rel is missing"
			[ "$(sha "$placed/$rel")" = "$(sha "$tree/$rel")" ] || fail "$placed/$rel differs from $tree/$rel"
		done <<EOF
$(cd "$tree" && find . -type f | sed 's/^\.\///')
EOF
		pass "$item placed intact under $placed"
	done
done

# --- Out of the manifest ------------------------------------------------------------

step "With harness_empty_manifest the next run removes every file"
stop_daemon
sed 's/^manifest: .*/manifest: harness_empty_manifest/' "$SAVE_DIR/config.yaml" >"$CONFIG" || fail "could not rewrite $CONFIG"
start_daemon
deadline=$(($(date +%s) + TIMEOUT_SEC))
until all_gone || [ "$(date +%s)" -ge "$deadline" ]; do
	sleep 3
done
while IFS= read -r target; do
	[ ! -e "$target" ] || fail "$target still exists ${TIMEOUT_SEC}s after its item left the manifest"
done <<EOF
$TARGETS
EOF
pass "$(printf '%s\n' "$TARGETS" | wc -l | tr -d ' ') files gone"

echo
printf '\033[32mHARNESS SMOKE PASSED\033[0m\n'
exit 0
