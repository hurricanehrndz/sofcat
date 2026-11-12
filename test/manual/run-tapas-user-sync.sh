#!/bin/bash
# User-scope placement check for the Tapas Test fixture brand on macOS: the
# twin of run-tapas-user-sync.ps1.
#
# Runs the installed per-user helper, tapas-test-useragent, and asserts that
# it placed tapas-user-plugin in this user's home: the exclusive
# tapas-user-plugins root under ~/.agents, which the helper creates and marks
# as its own, the tree under its plugins items_dir, its receipt under
# ~/Library/Application Support/Tapas Test/receipts with the plan's managed
# flag, and an index.json rendered with this account's login name, the
# placement's vars and its items; once deleted, that file is back after a
# second run. Nothing under ~/.agents may be owned by root: the daemon must
# never write there.
#
# Run AFTER run-tapas-smoke.sh has passed (a daemon run has staged the
# archive and stored the plan), as the user signed in at the console, not
# root (AGENTS.md, "macOS validation loop"):
#   test/manual/run-tapas-user-sync.sh
#
# Exits non-zero on the first failed assertion; prints each step and
# TAPAS USER SYNC PASSED at the end. Bash 3.2 compatible (the macOS /bin/bash).

set -u

SLUG="${SLUG:-tapas-test}"
PRODUCT="${PRODUCT:-Tapas Test}"
AGENT="${AGENT:-/Library/Application Support/$PRODUCT/bin/$SLUG-useragent}"
ITEM="${ITEM:-tapas-user-plugin}"
PLACEMENT="${PLACEMENT:-tapas-user-plugins}"
TIMEOUT_SEC="${TIMEOUT_SEC:-120}"

STATE_DIR="$HOME/Library/Application Support/$PRODUCT"
AGENTS_DIR="$HOME/.agents"
ROOT="$AGENTS_DIR/tapas-plugins"
TREE="$ROOT/plugins/$ITEM"
RECEIPT="$STATE_DIR/receipts/$PLACEMENT/$ITEM.json"
INDEX="$ROOT/index.json"
MARKER="$ROOT/.$SLUG-owned"
USER_NAME=$(id -un)

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

# run_agent runs the helper once and fails unless it exits 0 in time.
run_agent() {
	"$AGENT" &
	local pid=$! waited=0
	while kill -0 "$pid" 2>/dev/null; do
		[ "$waited" -lt "$TIMEOUT_SEC" ] || {
			kill "$pid"
			fail "the agent did not exit within ${TIMEOUT_SEC}s"
		}
		sleep 1
		waited=$((waited + 1))
	done
	wait "$pid" || fail "the agent exited $?"
	pass "the agent exited 0"
}

[ "$(id -u)" != 0 ] || fail "run as the signed-in user, not root"
command -v jq >/dev/null || fail "jq is missing"
[ -x "$AGENT" ] || fail "$AGENT is missing; install the fixture brand's .pkg first"

step "Start without the plugin root and its receipt"
# The whole root goes, marker included, so the helper must create and mark
# it again for the exclusive placement to accept it.
rm -rf "$ROOT" "$RECEIPT"
pass "removed $ROOT and $RECEIPT"

step "The helper places $ITEM under the marked root"
# The helper always logs to useragent.log in its state directory; this adds
# its debug records.
export SOFCAT_DEBUG=1
run_agent
[ -f "$MARKER" ] || fail "marker $MARKER is missing: the exclusive root is not the helper's"
[ -f "$TREE/SKILL.md" ] || fail "$TREE/SKILL.md is missing"
[ ! -e "$ROOT/$ITEM" ] || fail "$ITEM was placed at the root, not under items_dir"
[ -f "$RECEIPT" ] || fail "receipt $RECEIPT is missing"
jq -e --arg i "$ITEM" --arg p "$PLACEMENT" --arg path "$TREE" \
	'.item == $i and .placement == $p and .version == "1.0.0" and .path == $path and .managed == true
	and (.tree_hash | test("^sha256:[0-9a-f]{64}$")) and .placed_hash == .tree_hash and .rendered == []
	and (.installed_at_utc | length) > 0' \
	"$RECEIPT" >/dev/null || fail "receipt $RECEIPT does not match: $(cat "$RECEIPT")"
[ -f "$STATE_DIR/useragent.log" ] || fail "$STATE_DIR/useragent.log is missing"
pass "placed $TREE under the marked root $ROOT; receipt managed=true"

step "index.json names $USER_NAME, tapas-user and $ITEM"
[ -f "$INDEX" ] || fail "$INDEX is missing"
index_text=$(cat "$INDEX")
jq -e --arg u "$USER_NAME" --arg root "$ROOT" --arg i "$ITEM" \
	'.user == $u and .name == "tapas-user" and .root == $root and .plugins == [$i]' \
	"$INDEX" >/dev/null || fail "$INDEX reads '$index_text', want user $USER_NAME, name tapas-user, root $ROOT, plugins [$ITEM]"
pass "$INDEX matches"

step "A sync with no tree change regenerates a deleted index.json"
rm -f "$INDEX"
run_agent
[ -f "$INDEX" ] || fail "$INDEX was not restored by a second run"
[ "$(cat "$INDEX")" = "$index_text" ] || fail "$INDEX restored as '$(cat "$INDEX")', want '$index_text'"
pass "$INDEX restored as it was"

step "A key sofcat added to the user's settings is back after a helper run"
# run-tapas-smoke.sh seeded this file (the merge output of tapas-marketplace)
# and sofcat added the managed plugin's key to it. Deleting that key is what
# `claude plugin uninstall` does; the next sync adds it again, and the user's
# own keys stay as they were.
SETTINGS="$AGENTS_DIR/tapas-claude/settings.json"
MANAGED_KEY="tapas-managed-plugin@tapas-test"
if [ -f "$SETTINGS" ]; then
	tmp=$(mktemp)
	jq --arg k "$MANAGED_KEY" 'del(.enabledPlugins[$k])' "$SETTINGS" >"$tmp" && cat "$tmp" >"$SETTINGS"
	rm -f "$tmp"
	jq -e --arg k "$MANAGED_KEY" '.enabledPlugins | has($k) | not' "$SETTINGS" >/dev/null || fail "could not delete $MANAGED_KEY from $SETTINGS"
	run_agent
	jq -e --arg k "$MANAGED_KEY" '.enabledPlugins[$k] == true and .model == "keep-me" and .enabledPlugins["tapas-second-plugin@tapas-test"] == false' "$SETTINGS" >/dev/null ||
		fail "$SETTINGS after a run: $(cat "$SETTINGS"), want $MANAGED_KEY true, model keep-me, tapas-second-plugin@tapas-test false"
	pass "$MANAGED_KEY is back as true; model and the seeded false are as they were"
else
	pass "$SETTINGS is not there (run-tapas-smoke.sh did not seed it); skipped"
fi

step "Nothing under $AGENTS_DIR is owned by root"
owned=$(find "$AGENTS_DIR" -user root -print | head -n5)
[ -z "$owned" ] || fail "owned by root: $owned"
pass "$(find "$AGENTS_DIR" | wc -l | tr -d ' ') entries, none owned by root"

echo
printf '\033[32mTAPAS USER SYNC PASSED\033[0m\n'
exit 0
