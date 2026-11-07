#!/bin/bash
# Zip-item end-to-end smoke test for the Tapas Test fixture brand on macOS:
# the twin of run-tapas-smoke.ps1 without the Windows user-helper and
# Chocolatey steps.
#
# It drives the running Tapas Test launchd daemon over its socket (through
# `tapas-test -c <config> -S ...`) against the repository
# `just bootstrap-tapas-macos` lays out from test/manual/fixtures/tapas, and
# asserts on the placed trees and receipts: the daemon's label, socket and
# root-only data directory, managed and self-service placement, removal,
# adoption of a hand-placed tree, conflict reporting for a user edit, the
# exclusive-root marker and refusal, a placement outside the write roots, the
# marketplace plugins with the generated marketplace.json and settings drop-in,
# a skill deferred until a Munki report lists its required software, a held
# conditional item, scope exit, and the zip-only build's rejection of an exe
# item and a skill with a postinstall_script.
#
# Run once on a clean Mac as root, after installing the fixture brand's .pkg,
# writing its config.yaml and kickstarting the daemon (AGENTS.md, "macOS
# validation loop"):
#   sudo test/manual/run-tapas-smoke.sh
#
# The Munki steps must not touch the Mac's real Munki report, so the smoke
# adds SOFCAT_TEST_MUNKI_REPORT (a test hook read by pkg/software) to the
# daemon's plist and restarts the daemon first, and removes it again at the
# end. A failed run leaves it in the plist until the .pkg is reinstalled.
#
# Exits non-zero on the first failed assertion; prints each step and
# TAPAS SMOKE PASSED at the end. Bash 3.2 compatible (the macOS /bin/bash).

set -u

SLUG="${SLUG:-tapas-test}"
PRODUCT="${PRODUCT:-Tapas Test}"
LABEL="${LABEL:-ca.hrndz.tapas-test}"
SUPPORT="/Library/Application Support/$PRODUCT"
SOFCAT="${SOFCAT:-$SUPPORT/bin/$SLUG}"
CONFIG="${CONFIG:-$SUPPORT/config.yaml}"
DATA_DIR="${DATA_DIR:-$SUPPORT/Data}"
PLIST="/Library/LaunchDaemons/$LABEL.plist"
SOCKET="/var/run/$SLUG/$SLUG-service.sock"
# The brand's write root and a sibling outside it; see
# test/manual/fixtures/tapas/brand/write-roots.yaml and harnesses.yaml.
SMOKE_ROOT="$SUPPORT"
OUTSIDE_ROOT="/Library/Application Support/tapas-test-outside"
MUNKI_DIR="/Library/Application Support/tapas-test-munki"
MUNKI_REPORT="$MUNKI_DIR/ManagedInstallReport.plist"
TIMEOUT_SEC="${TIMEOUT_SEC:-120}"

RECEIPT_DIR="$DATA_DIR/receipts"
INVENTORY="$DATA_DIR/inventory.json"
LOG_PATH="$DATA_DIR/sofcat.log"
SELF_SERVE="$DATA_DIR/service-manifest.yaml"
CONDITIONS_DIR="$DATA_DIR/conditions"
CONDITION_SCRIPT="$CONDITIONS_DIR/10-tapas-smoke"

CLAUDE_ROOT="$SMOKE_ROOT/claude/skills"
CODEX_ROOT="$SMOKE_ROOT/codex/skills"
EXCLUSIVE_ROOT="$SMOKE_ROOT/exclusive/skills"
FOREIGN_ROOT="$SMOKE_ROOT/foreign/skills"
MARKET_ROOT="$SMOKE_ROOT/marketplace"
PLUGINS_ROOT="$MARKET_ROOT/plugins"
MARKETPLACE_JSON="$MARKET_ROOT/.claude-plugin/marketplace.json"
DROP_IN="$SMOKE_ROOT/claude/managed-settings.d/50-tapas-test.json"
SCRIPTED_MARKER="$SMOKE_ROOT/scripted-skill-ran.txt"

step_num=0
OUT=""
CODE=0

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

# sofcat runs a service command into OUT and CODE. A non-zero exit fails the
# smoke unless the second argument is allow-fail.
sofcat() {
	OUT=$("$SOFCAT" -c "$CONFIG" -S "$1" 2>&1)
	CODE=$?
	# The log goes to stderr, so $(optional_item ...) captures only the item.
	printf '\033[90m    %s -S %s (exit %d)\033[0m\n' "$SLUG" "$1" "$CODE" >&2
	printf '%s\n' "$OUT" | sed 's/^/      /' >&2
	if [ "${2:-}" != allow-fail ] && [ "$CODE" -ne 0 ]; then
		fail "$SLUG -S $1 exited $CODE"
	fi
}

# json_lines prints the JSON objects in OUT, one per line.
json_lines() {
	printf '%s\n' "$OUT" | sed -n 's/^[[:space:]]*\({.*}\)[[:space:]]*$/\1/p'
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

# yaml_list prints a top-level YAML sequence ("key:\n  - a" or "key: [a, b]"),
# one value per line. Good enough for the flat self-serve manifest.
yaml_list() {
	[ -f "$1" ] || return 0
	awk -v key="$2" '
		/^[[:space:]]*#/ { next }
		$0 ~ "^" key ":" {
			inline = substr($0, length(key) + 2)
			gsub(/^[[:space:]]+|[[:space:]]+$/, "", inline)
			if (inline != "" && inline != "[]") {
				gsub(/[\[\]]/, "", inline)
				n = split(inline, parts, ",")
				for (i = 1; i <= n; i++) { v = parts[i]; gsub(/^[[:space:]]+|[[:space:]]+$/, "", v); if (v != "") print v }
				inkey = 0
			} else { inkey = 1 }
			next
		}
		inkey && /^[[:space:]]*-[[:space:]]*/ { v = $0; sub(/^[[:space:]]*-[[:space:]]*/, "", v); gsub(/[[:space:]]+$/, "", v); print v; next }
		inkey && /^[^[:space:]]/ { inkey = 0 }
	' "$1"
}

selected() {
	yaml_list "$SELF_SERVE" managed_installs | grep -qx "$1"
}

pruned() {
	[ -z "$(yaml_list "$SELF_SERVE" managed_uninstalls)" ]
}

# optional_item prints the ListOptionalInstalls object for one item, if listed.
optional_item() {
	sofcat ListOptionalInstalls
	json_lines | jq -c --arg n "$1" 'select(.itemName == $n)' 2>/dev/null | head -n1
}

# operation sends an InstallItem/RemoveItem command, streams the operation to
# its end, and prints the terminal status record.
TERMINAL=""
operation() {
	sofcat "$1"
	wait_operation "$1"
}

wait_operation() {
	local op
	op=$(printf '%s\n' "$OUT" | sed -n 's/.*operationId:[[:space:]]*\([^[:space:]]*\).*/\1/p' | head -n1)
	[ -n "$op" ] || fail "no operationId returned for $1"
	sofcat "StreamOperationStatus:$op"
	TERMINAL=$(json_lines | jq -c --arg op "$op" \
		'select(.operationId == $op and (.state == "Succeeded" or .state == "Failed" or .state == "Deferred" or .state == "Canceled"))' \
		2>/dev/null | tail -n1)
	[ -n "$TERMINAL" ] || fail "no terminal event received for $1"
}

terminal() { printf '%s' "$TERMINAL" | jq -r "$1"; }

assert_succeeded() {
	operation "$1"
	[ "$(terminal .state)" = Succeeded ] || fail "$1 ended $(terminal .state): $TERMINAL"
}

assert_failed_with() {
	operation "$1"
	if [ "$(terminal .state)" != Failed ] || [ "$(terminal .errorCode)" != "$2" ]; then
		fail "$1 expected Failed/$2, got $TERMINAL"
	fi
	terminal .errorMessage | grep -qF -- "$3" || fail "$1 error does not mention $3: $(terminal .errorMessage)"
}

# invoke_run triggers a full run with an idempotent request (the adopted skill
# stays installed from the adoption step on) and waits for it to end. A run
# refreshes the admin facts and reconciles every item.
invoke_run() {
	assert_succeeded "InstallItem:tapas-adopt-skill"
}

receipt_path() { printf '%s/%s/%s.json' "$RECEIPT_DIR" "$1" "$2"; }

# assert_placed ROOT PLACEMENT ITEM [FILE] checks a tree and its receipt.
assert_placed() {
	local tree="$1/$3" file="${4:-SKILL.md}" rc
	rc=$(receipt_path "$2" "$3")
	[ -f "$tree/$file" ] || fail "$tree/$file is missing"
	[ -f "$rc" ] || fail "receipt $rc is missing"
	# No fixture item ships a template yet, so every tree is placed as
	# shipped: placed_hash equals tree_hash and nothing is rendered.
	jq -e --arg i "$3" --arg p "$2" --arg path "$tree" \
		'.item == $i and .placement == $p and .version == "1.0.0" and .path == $path
		and (.tree_hash | test("^sha256:[0-9a-f]{64}$"))
		and (.placed_hash | test("^sha256:[0-9a-f]{64}$")) and .placed_hash == .tree_hash
		and .rendered == []' \
		"$rc" >/dev/null || fail "receipt $rc does not match: $(cat "$rc")"
}

# receipt_times lists every receipt with its modification time.
receipt_times() {
	find "$RECEIPT_DIR" -type f -name '*.json' | sort | while read -r f; do
		printf '%s %s\n' "$(stat -f %m "$f")" "$f"
	done
}

assert_not_placed() {
	[ ! -e "$1/$3" ] || fail "$1/$3 exists"
	[ ! -e "$(receipt_path "$2" "$3")" ] || fail "receipt $(receipt_path "$2" "$3") exists"
}

gone() { [ ! -e "$1" ] && [ ! -e "$2" ]; }

wait_removed() {
	wait_for "$3 to leave $2 with its receipt" gone "$1/$3" "$(receipt_path "$2" "$3")"
}

wait_pruned() {
	wait_for "managed_uninstalls to be pruned empty" pruned
}

# assert_logged_rejection ITEM prints the log line saying the zip-only build
# rejected ITEM.
assert_logged_rejection() {
	[ -f "$LOG_PATH" ] || fail "$LOG_PATH is missing"
	local line
	line=$(grep -F "rejected by this zip-only build" "$LOG_PATH" | grep -F -- "$1" | head -n1)
	[ -n "$line" ] || fail "$LOG_PATH does not say $1 was rejected for the zip-only build"
	printf '%s' "$line"
}

warned_held() {
	jq -e --arg f "$1" '[.Warnings[]? | select(test($f) and test("held"))] | length > 0' "$INVENTORY" >/dev/null
}

sha() { shasum -a 256 "$1" | awk '{print $1}'; }

# --- Preconditions and the daemon ---------------------------------------------

[ "$(id -u)" = 0 ] || fail "run as root: sudo $0"
for tool in jq shasum /usr/libexec/PlistBuddy; do
	command -v "$tool" >/dev/null || fail "$tool is missing"
done
[ -x "$SOFCAT" ] || fail "$SOFCAT is missing; install the fixture brand's .pkg first"
[ -f "$CONFIG" ] || fail "$CONFIG is missing; write the smoke config first"
[ -f "$PLIST" ] || fail "$PLIST is missing; install the fixture brand's .pkg first"

step "Point the daemon at the smoke's Munki report and restart it"
[ ! -e "$MUNKI_DIR" ] || fail "$MUNKI_DIR exists before the smoke created it (not a clean Mac?)"
if /usr/libexec/PlistBuddy -c "Print :EnvironmentVariables:SOFCAT_TEST_MUNKI_REPORT" "$PLIST" >/dev/null 2>&1; then
	/usr/libexec/PlistBuddy -c "Set :EnvironmentVariables:SOFCAT_TEST_MUNKI_REPORT $MUNKI_REPORT" "$PLIST"
else
	/usr/libexec/PlistBuddy -c "Add :EnvironmentVariables dict" "$PLIST" 2>/dev/null
	/usr/libexec/PlistBuddy -c "Add :EnvironmentVariables:SOFCAT_TEST_MUNKI_REPORT string $MUNKI_REPORT" "$PLIST" ||
		fail "could not add SOFCAT_TEST_MUNKI_REPORT to $PLIST"
fi
launchctl bootout "system/$LABEL" 2>/dev/null
bootstrapped() { launchctl bootstrap system "$PLIST" 2>/dev/null; }
wait_for "launchctl bootstrap system $PLIST" bootstrapped
pass "$PLIST sets SOFCAT_TEST_MUNKI_REPORT=$MUNKI_REPORT; daemon bootstrapped"

step "launchd runs the daemon as $LABEL, serving $SOCKET"
running() { launchctl print "system/$LABEL" 2>/dev/null | grep -q 'state = running'; }
wait_for "system/$LABEL to be running" running
answers() { [ -S "$SOCKET" ] && "$SOFCAT" -c "$CONFIG" -S GetServiceInfo >/dev/null 2>&1; }
wait_for "the daemon to answer on $SOCKET" answers
sofcat GetServiceInfo
pass "system/$LABEL running; $SOCKET answers"

step "The data directory is root-owned with mode 0700"
perm=$(stat -f '%Su:%Sg %Lp' "$DATA_DIR")
[ "$perm" = "root:wheel 700" ] || fail "$DATA_DIR is $perm, want root:wheel 700"
pass "$DATA_DIR is $perm"

# --- The startup run ------------------------------------------------------------

step "The startup run places the managed skill with a receipt"
managed_rc=$(receipt_path claude-skills tapas-managed-skill)
startup_done() { [ -f "$managed_rc" ] && [ -f "$INVENTORY" ]; }
wait_for "the managed skill's receipt and inventory.json" startup_done
assert_placed "$CLAUDE_ROOT" claude-skills tapas-managed-skill
jq -e '.managed == true' "$managed_rc" >/dev/null || fail "the managed skill's receipt has managed=false"
pass "tapas-managed-skill placed under $CLAUDE_ROOT, receipt managed=true"

step "The Codex placement is per machine on macOS"
assert_placed "$CODEX_ROOT" codex-skills tapas-managed-skill
jq -e '[.ManagedInstalls[] | select(.name == "tapas-managed-skill" and .status == "installed")] | length == 1' "$INVENTORY" >/dev/null ||
	fail "inventory does not record tapas-managed-skill as installed"
pass "tapas-managed-skill placed under $CODEX_ROOT; installed"

step "The built-in facts are the macOS ones"
jq -e '.Conditions.os == "darwin" and .Conditions.machine_manufacturer == "Apple" and (.Conditions.os_version | type == "string") and (.Conditions | has("os_build") | not)' "$INVENTORY" >/dev/null ||
	fail "inventory Conditions: $(jq -c '.Conditions' "$INVENTORY")"
pass "$(jq -c '.Conditions | {os, os_version, machine_model, machine_type, machine_manufacturer}' "$INVENTORY")"

step "The exclusive placement root sofcat created carries the .$SLUG-owned marker"
assert_placed "$EXCLUSIVE_ROOT" exclusive-skills tapas-exclusive-skill
[ -f "$EXCLUSIVE_ROOT/.$SLUG-owned" ] || fail "marker $EXCLUSIVE_ROOT/.$SLUG-owned is missing"
pass "tapas-exclusive-skill placed and $EXCLUSIVE_ROOT/.$SLUG-owned present"

step "Held conditional items are not placed while their facts are missing"
[ ! -e "$CONDITION_SCRIPT" ] || fail "$CONDITION_SCRIPT exists before the smoke wrote it (not a clean Mac?)"
assert_not_placed "$CLAUDE_ROOT" claude-skills tapas-scoped-skill
assert_not_placed "$CLAUDE_ROOT" claude-skills tapas-held-skill
for fact in tapas_smoke_scope tapas_smoke_held; do
	warned_held "$fact" || fail "inventory does not warn that the $fact block is held: $(jq -c '.Warnings' "$INVENTORY")"
done
pass "tapas-scoped-skill and tapas-held-skill held, not placed"

# --- Zip-only build ---------------------------------------------------------------

step "The zip-only build skips the managed exe item and logs why"
jq -e '[.ManagedInstalls[] | select(.name == "tapas-exe-item" and .status == "installed")] | length == 0' "$INVENTORY" >/dev/null ||
	fail "inventory records tapas-exe-item as installed"
line=$(assert_logged_rejection tapas-exe-item) || exit 1
printf '%s' "$line" | grep -Eq 'installer\.type .{1,4}exe.{1,4} is not zip' || fail "the rejection does not give the reason: $line"
pass "tapas-exe-item not installed; log: $line"

step "The zip-only build neither lists nor installs the skill with a postinstall_script"
[ -z "$(optional_item tapas-scripted-skill)" ] || fail "ListOptionalInstalls lists tapas-scripted-skill"
[ -n "$(optional_item tapas-optional-skill)" ] || fail "ListOptionalInstalls no longer lists tapas-optional-skill"
sofcat InstallItem:tapas-scripted-skill allow-fail
[ "$CODE" -ne 0 ] || fail "InstallItem:tapas-scripted-skill was accepted"
printf '%s' "$OUT" | grep -Eq 'item_not_available|not available for self-service' ||
	fail "InstallItem:tapas-scripted-skill was not refused as item_not_available: $OUT"
! selected tapas-scripted-skill || fail "the refused tapas-scripted-skill is in the self-serve managed_installs"
assert_not_placed "$CLAUDE_ROOT" claude-skills tapas-scripted-skill
[ ! -e "$SCRIPTED_MARKER" ] || fail "tapas-scripted-skill's postinstall_script ran: $SCRIPTED_MARKER exists"
line=$(assert_logged_rejection tapas-scripted-skill) || exit 1
printf '%s' "$line" | grep -q postinstall_script || fail "the rejection does not give the reason: $line"
pass "not listed, InstallItem refused, nothing placed; log: $line"

# --- Self-service ---------------------------------------------------------------

step "ListOptionalInstalls offers the optional skill with a real status"
opt=$(optional_item tapas-optional-skill)
[ -n "$opt" ] || fail "tapas-optional-skill not listed by ListOptionalInstalls"
[ "$(printf '%s' "$opt" | jq -r .status)" = NotInstalled ] || fail "tapas-optional-skill: $opt, expected NotInstalled"
pass "tapas-optional-skill offered, status=NotInstalled"

step "InstallItem places the optional skill with a receipt"
assert_succeeded InstallItem:tapas-optional-skill
assert_placed "$CLAUDE_ROOT" claude-skills tapas-optional-skill
[ "$(optional_item tapas-optional-skill | jq -r .status)" = Installed ] || fail "tapas-optional-skill is not Installed after install"
pass "tapas-optional-skill placed, status=Installed"

step "RemoveItem removes the optional skill and its receipt"
assert_succeeded RemoveItem:tapas-optional-skill
wait_removed "$CLAUDE_ROOT" claude-skills tapas-optional-skill
wait_pruned
pass "tree and receipt gone, managed_uninstalls pruned"

# --- Marketplace and settings drop-in -----------------------------------------------

plugin_entries() { jq -c --arg n "$1" '[.plugins[] | select(.name == $n)]' "$MARKETPLACE_JSON"; }
enabled() { jq -e --arg k "$1" '.enabledPlugins[$k] == true' "$DROP_IN" >/dev/null; }

step "The startup run places the managed plugin and generates marketplace.json"
assert_placed "$PLUGINS_ROOT" tapas-marketplace tapas-managed-plugin .claude-plugin/plugin.json
[ -f "$MARKET_ROOT/.$SLUG-owned" ] || fail "the marketplace root has no .$SLUG-owned marker"
[ -f "$MARKETPLACE_JSON" ] || fail "$MARKETPLACE_JSON is missing"
jq -e '.name == "tapas-test" and .owner.name == "Tapas Test" and .renames["old-plugin"] == "tapas-managed-plugin" and .allowCrossMarketplaceDependenciesOn == ["other"]' \
	"$MARKETPLACE_JSON" >/dev/null || fail "marketplace.json: $(jq -c . "$MARKETPLACE_JSON")"
plugin_entries tapas-managed-plugin | jq -e 'length == 1 and .[0].source == "./plugins/tapas-managed-plugin" and .[0].version == "1.0.0" and .[0].defaultEnabled == true' >/dev/null ||
	fail "tapas-managed-plugin entry: $(plugin_entries tapas-managed-plugin)"
[ "$(plugin_entries tapas-optional-plugin)" = "[]" ] || fail "the unselected optional plugin is listed"
pass "plugins/tapas-managed-plugin placed; listed with defaultEnabled=true; renames and allowCrossMarketplaceDependenciesOn as configured"

step "The drop-in registers the marketplace and enables the managed plugin"
[ -f "$DROP_IN" ] || fail "$DROP_IN is missing"
jq -e --arg p "$MARKET_ROOT" '.extraKnownMarketplaces["tapas-test"].source == {source: "directory", path: $p}' "$DROP_IN" >/dev/null ||
	fail "extraKnownMarketplaces.tapas-test: $(jq -c '.extraKnownMarketplaces' "$DROP_IN"), want directory $MARKET_ROOT"
enabled tapas-managed-plugin@tapas-test || fail "enabledPlugins lacks tapas-managed-plugin@tapas-test: $(jq -c '.enabledPlugins' "$DROP_IN")"
count=$(find "$(dirname "$DROP_IN")" -mindepth 1 -maxdepth 1 | wc -l | tr -d ' ')
[ "$count" = 1 ] || fail "managed-settings.d holds more than the drop-in: $(ls -A "$(dirname "$DROP_IN")")"
pass "$DROP_IN registers $MARKET_ROOT and enables tapas-managed-plugin@tapas-test"

step "InstallItem adds the optional plugin, listed but not enabled"
assert_succeeded InstallItem:tapas-optional-plugin
assert_placed "$PLUGINS_ROOT" tapas-marketplace tapas-optional-plugin .claude-plugin/plugin.json
plugin_entries tapas-optional-plugin | jq -e 'length == 1 and .[0].defaultEnabled == false' >/dev/null ||
	fail "tapas-optional-plugin entry: $(plugin_entries tapas-optional-plugin)"
! enabled tapas-optional-plugin@tapas-test || fail "the optional plugin is in enabledPlugins"
enabled tapas-managed-plugin@tapas-test || fail "the managed plugin left enabledPlugins"
pass "tapas-optional-plugin listed with defaultEnabled=false, not in enabledPlugins"

step "RemoveItem drops the optional plugin's entry"
assert_succeeded RemoveItem:tapas-optional-plugin
wait_removed "$PLUGINS_ROOT" tapas-marketplace tapas-optional-plugin
wait_pruned
[ "$(plugin_entries tapas-optional-plugin)" = "[]" ] || fail "tapas-optional-plugin still listed in marketplace.json"
[ "$(plugin_entries tapas-managed-plugin | jq length)" = 1 ] || fail "tapas-managed-plugin no longer listed"
enabled tapas-managed-plugin@tapas-test || fail "the managed plugin left enabledPlugins"
pass "tree, receipt and entry gone; the managed plugin stays enabled"

# --- Required software -------------------------------------------------------------

# tapas-choco-skill requires_software Demo Tool, Munki item DemoTool.
step "A skill whose required Munki item is missing is deferred and nothing is written"
operation InstallItem:tapas-choco-skill
if [ "$(terminal .state)" != Deferred ] || [ "$(terminal .errorCode)" != missing_software ]; then
	fail "InstallItem:tapas-choco-skill expected Deferred/missing_software, got $TERMINAL"
fi
want_message="Needs Demo Tool. Install it first, then try again."
terminal .errorMessage | grep -qF "$want_message" || fail "the deferral message is not '$want_message': $(terminal .errorMessage)"
assert_not_placed "$CLAUDE_ROOT" claude-skills tapas-choco-skill
selected tapas-choco-skill || fail "the deferred tapas-choco-skill left the self-serve managed_installs, so no later run retries it"
pass "Deferred/missing_software: $(terminal .errorMessage); nothing placed; still selected"

step "Once the Munki report lists DemoTool, the next run places the skill"
mkdir -p "$MUNKI_DIR"
cat >"$MUNKI_REPORT" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>InstalledItems</key>
	<array>
		<string>DemoTool</string>
	</array>
</dict>
</plist>
EOF
assert_succeeded InstallItem:tapas-choco-skill
assert_placed "$CLAUDE_ROOT" claude-skills tapas-choco-skill
pass "tapas-choco-skill placed with a receipt"

step "RemoveItem removes the skill that needed Demo Tool"
assert_succeeded RemoveItem:tapas-choco-skill
wait_removed "$CLAUDE_ROOT" claude-skills tapas-choco-skill
wait_pruned
rm -rf "$MUNKI_DIR"
pass "tree and receipt gone; $MUNKI_DIR removed"

# --- Adoption --------------------------------------------------------------------

step "A catalog-equal tree placed by hand is adopted without being rewritten"
# Must equal test/manual/fixtures/tapas/rendered/tapas-adopt-skill/claude-skills/darwin/SKILL.md
# byte for byte, with mode 0644.
adopt_tree="$CLAUDE_ROOT/tapas-adopt-skill"
adopt_file="$adopt_tree/SKILL.md"
mkdir -p "$adopt_tree"
printf -- '---\nname: tapas-adopt-skill\ndescription: A self-service skill placed by hand and adopted\n---\n' >"$adopt_file"
chmod 0644 "$adopt_file"
before_hash=$(sha "$adopt_file")
before_time=$(stat -f %m "$adopt_file")
[ ! -e "$(receipt_path claude-skills tapas-adopt-skill)" ] || fail "the hand-placed tree already has a receipt"
assert_succeeded InstallItem:tapas-adopt-skill
assert_placed "$CLAUDE_ROOT" claude-skills tapas-adopt-skill
if [ "$(sha "$adopt_file")" != "$before_hash" ] || [ "$(stat -f %m "$adopt_file")" != "$before_time" ]; then
	fail "the adopted tree was rewritten"
fi
pass "receipt written, hand-placed tree untouched"

# --- Conflict ---------------------------------------------------------------------

step "A user edit fails the removal with placement_conflict and the tree stays"
assert_succeeded InstallItem:tapas-optional-skill
assert_placed "$CLAUDE_ROOT" claude-skills tapas-optional-skill
optional_file="$CLAUDE_ROOT/tapas-optional-skill/SKILL.md"
original=$(mktemp)
cp -p "$optional_file" "$original"
echo "edited by the tapas smoke" >>"$optional_file"
edited_hash=$(sha "$optional_file")
assert_failed_with RemoveItem:tapas-optional-skill placement_conflict claude-skills
[ -f "$optional_file" ] && [ "$(sha "$optional_file")" = "$edited_hash" ] || fail "the edited tree was changed or removed"
[ -f "$(receipt_path claude-skills tapas-optional-skill)" ] || fail "the conflicting tree's receipt was removed"
pass "Failed/placement_conflict; edited tree and receipt kept"

step "Restoring the file lets the next run finish the removal"
cat "$original" >"$optional_file"
rm -f "$original"
invoke_run
wait_removed "$CLAUDE_ROOT" claude-skills tapas-optional-skill
wait_pruned
pass "tree and receipt gone after the restore"

# --- Refusals ----------------------------------------------------------------------

step "An exclusive root with files sofcat did not put there is refused"
mkdir -p "$FOREIGN_ROOT"
echo "not sofcat's" >"$FOREIGN_ROOT/someone-else.txt"
assert_failed_with InstallItem:tapas-foreign-skill placement_refused foreign-skills
assert_not_placed "$FOREIGN_ROOT" foreign-skills tapas-foreign-skill
[ ! -e "$FOREIGN_ROOT/.$SLUG-owned" ] || fail "sofcat marked a root it does not own"
assert_succeeded RemoveItem:tapas-foreign-skill
wait_pruned
pass "Failed/placement_refused; nothing written, no marker"

step "A placement outside the write roots is refused"
assert_failed_with InstallItem:tapas-outside-skill placement_refused "outside the write roots"
[ ! -e "$OUTSIDE_ROOT" ] || fail "$OUTSIDE_ROOT was created"
assert_succeeded RemoveItem:tapas-outside-skill
wait_pruned
pass "Failed/placement_refused; $OUTSIDE_ROOT not created"

# --- Scope exit -------------------------------------------------------------------

# write_condition_script JSON writes the admin fact script: root-owned, 0755.
write_condition_script() {
	mkdir -p "$CONDITIONS_DIR"
	printf '#!/bin/sh\necho %s\n' "'$1'" >"$CONDITION_SCRIPT"
	chown root:wheel "$CONDITION_SCRIPT"
	chmod 0755 "$CONDITION_SCRIPT"
}

step "An admin fact brings the conditional items into scope"
write_condition_script '{"tapas_smoke_scope": "in", "tapas_smoke_held": "yes"}'
invoke_run
assert_placed "$CLAUDE_ROOT" claude-skills tapas-scoped-skill
assert_placed "$CLAUDE_ROOT" claude-skills tapas-held-skill
pass "tapas-scoped-skill and tapas-held-skill placed"

step "Scope exit removes the item that left the desired set and keeps the held one"
# tapas_smoke_scope turns false; tapas_smoke_held goes missing, so its block is held.
write_condition_script '{"tapas_smoke_scope": "out"}'
invoke_run
wait_removed "$CLAUDE_ROOT" claude-skills tapas-scoped-skill
assert_placed "$CLAUDE_ROOT" claude-skills tapas-held-skill
warned_held tapas_smoke_held || fail "inventory does not warn that the tapas_smoke_held block is held: $(jq -c '.Warnings' "$INVENTORY")"
assert_placed "$CLAUDE_ROOT" claude-skills tapas-managed-skill
assert_not_placed "$CLAUDE_ROOT" claude-skills tapas-scripted-skill
[ ! -e "$SCRIPTED_MARKER" ] || fail "tapas-scripted-skill's postinstall_script ran: $SCRIPTED_MARKER exists"
pass "tapas-scoped-skill removed; tapas-held-skill and tapas-managed-skill kept; tapas-scripted-skill never placed"

step "A condition script a user could change is skipped"
write_condition_script '{"tapas_smoke_scope": "in", "tapas_smoke_held": "yes"}'
chmod 0777 "$CONDITION_SCRIPT"
invoke_run
assert_not_placed "$CLAUDE_ROOT" claude-skills tapas-scoped-skill
grep -F "skipping file in the conditions directory" "$LOG_PATH" | grep -qF "10-tapas-smoke" ||
	fail "$LOG_PATH does not say the world-writable script was skipped"
pass "world-writable $CONDITION_SCRIPT skipped; tapas-scoped-skill not placed"

# --- No-op run --------------------------------------------------------------------

step "A run with nothing changed rewrites no receipt"
before=$(receipt_times)
[ -n "$before" ] || fail "no receipts under $RECEIPT_DIR"
invoke_run
after=$(receipt_times)
[ "$after" = "$before" ] || fail "the run changed a receipt: before
$before
after
$after"
pass "$(printf '%s\n' "$before" | wc -l | tr -d ' ') receipts untouched"

step "Take SOFCAT_TEST_MUNKI_REPORT out of the daemon's plist again"
/usr/libexec/PlistBuddy -c "Delete :EnvironmentVariables:SOFCAT_TEST_MUNKI_REPORT" "$PLIST" ||
	fail "could not remove SOFCAT_TEST_MUNKI_REPORT from $PLIST"
# launchd reads the plist only at bootstrap, so a kickstart would keep the
# old environment.
launchctl bootout "system/$LABEL" 2>/dev/null
wait_for "launchctl bootstrap system $PLIST" bootstrapped
wait_for "system/$LABEL to be running" running
pass "$PLIST no longer sets SOFCAT_TEST_MUNKI_REPORT; daemon running"

echo
printf '\033[32mTAPAS SMOKE PASSED\033[0m\n'
exit 0
