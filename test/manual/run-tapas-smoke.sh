#!/bin/bash
# Zip-item end-to-end smoke test for the Tapas Test fixture brand on macOS:
# the twin of run-tapas-smoke.ps1, with Munki in place of its marker file. The per-user
# helper part checks the daemon starts it through its LaunchAgent for the user
# signed in to the GUI; run-tapas-user-sync.sh checks what it places.
#
# It drives the running Tapas Test launchd daemon over its socket (through
# `tapas-test -c <config> -S ...`) against the repository
# `just bootstrap-tapas-macos` lays out from test/manual/fixtures/tapas, and
# asserts on the placed trees and receipts: the daemon's label, socket and
# root-only data directory, managed and self-service placement, removal,
# adoption of a hand-placed tree, conflict reporting for a user edit, the
# exclusive-root marker and refusal, a placement outside the write roots, the
# marketplace plugins with the generated marketplace.json, the templated Codex
# catalog and skills index and the settings drop-in, a skill deferred until a
# Munki report lists its depends_check software, a held
# conditional item, scope exit, the Claude Desktop plugins whose trees ship
# templates (plugin.json from the managed flag, .mcp.json from an admin fact,
# re-rendered in place when either changes), and the zip-only build's
# rejection of an exe item.
#
# Run once on a clean Mac as root, with a user signed in to the GUI,
# after installing the fixture brand's .pkg,
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
# test/manual/fixtures/tapas/brand/write-roots.yaml and placements.yaml.
SMOKE_ROOT="$SUPPORT"
OUTSIDE_ROOT="/Library/Application Support/tapas-test-outside"
# The managed preferences file whose WriteRoots dictionary adds write roots.
POLICY_PLIST="/Library/Managed Preferences/$LABEL.plist"
MUNKI_DIR="/Library/Application Support/tapas-test-munki"
MUNKI_REPORT="$MUNKI_DIR/ManagedInstallReport.plist"
TIMEOUT_SEC="${TIMEOUT_SEC:-120}"

RECEIPT_DIR="$DATA_DIR/receipts"
INVENTORY="$DATA_DIR/inventory.json"
LOG_PATH="$DATA_DIR/sofcat.log"
SELF_SERVE="$DATA_DIR/service-manifest.yaml"
CONDITIONS_DIR="$DATA_DIR/conditions"
CONDITION_SCRIPT="$CONDITIONS_DIR/10-tapas-smoke"
# The admin fact script the Desktop plugins' templates and the
# tapas_desktop_managed conditional block read; written before the daemon
# restarts, so its startup run has it.
DESKTOP_SCRIPT="$CONDITIONS_DIR/20-tapas-desktop"
# The fact script the fixture repository delivers, where the daemon syncs it.
SYNCED_SCRIPT="$DATA_DIR/conditions-synced/05-tapas-synced"

CLAUDE_ROOT="$SMOKE_ROOT/claude/skills"
CODEX_ROOT="$SMOKE_ROOT/codex/skills"
EXCLUSIVE_ROOT="$SMOKE_ROOT/exclusive/skills"
FOREIGN_ROOT="$SMOKE_ROOT/foreign/skills"
MARKET_ROOT="$SMOKE_ROOT/marketplace"
PLUGINS_ROOT="$MARKET_ROOT/plugins"
DESKTOP_ROOT="$SMOKE_ROOT/desktop/org-plugins"
MARKETPLACE_JSON="$MARKET_ROOT/.claude-plugin/marketplace.json"
DROP_IN="$SMOKE_ROOT/claude/managed-settings.d/50-tapas-test.json"
# Rendered from the templates in placements.yaml, without receipts.
CODEX_JSON="$MARKET_ROOT/.agents/plugins/marketplace.json"
SKILLS_INDEX="$SMOKE_ROOT/claude/skills-index.json"
EXCLUSIVE_INDEX="$SMOKE_ROOT/claude/exclusive-index.json"
# The per-user helper's LaunchAgent and what it places for the user signed in
# in a GUI session: tapas-user-plugins is the user-scope placement on macOS.
USER_ARTIFACTS="$SUPPORT/user-artifacts"
AGENT_LABEL="$LABEL.useragent"
# /dev/console can read as root under Screen Sharing; the GUI session's
# loginwindow runs as its user, as the daemon finds it.
CONSOLE_UID=$(ps -axo uid=,comm= | awk '$2 ~ /\/loginwindow$/ && $1 >= 500 { print $1; exit }')
CONSOLE_USER=$(id -un "$CONSOLE_UID" 2>/dev/null)
CONSOLE_HOME=$(dscl . -read "/Users/$CONSOLE_USER" NFSHomeDirectory 2>/dev/null | awk '{print $2}')
AGENT_TARGET="gui/$CONSOLE_UID/$AGENT_LABEL"
USER_TREE="$CONSOLE_HOME/.agents/tapas-plugins/plugins/tapas-user-plugin"
USER_RECEIPT="$CONSOLE_HOME/Library/Application Support/$PRODUCT/receipts/tapas-user-plugins/tapas-user-plugin.json"
# The user-scope merge file of tapas-marketplace (placements.yaml): the real
# path would be ~/.claude/settings.json. The record lists the keys sofcat
# added to it, per user.
USER_SETTINGS="$CONSOLE_HOME/.agents/tapas-claude/settings.json"
MERGE_RECORD="$CONSOLE_HOME/Library/Application Support/$PRODUCT/receipts/.merged-keys.json"

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

# assert_placed ROOT PLACEMENT ITEM [FILE] [rendered] checks a tree and its
# receipt. A tree that ships templates (rendered) has a placed_hash of its own
# and rendered records; any other is placed as shipped.
assert_placed() {
	local tree="$1/$3" file="${4:-SKILL.md}" rc hashes
	rc=$(receipt_path "$2" "$3")
	[ -f "$tree/$file" ] || fail "$tree/$file is missing"
	[ -f "$rc" ] || fail "receipt $rc is missing"
	if [ "${5:-}" = rendered ]; then
		hashes='.placed_hash != .tree_hash and (.rendered | length) > 0'
	else
		hashes='.placed_hash == .tree_hash and .rendered == []'
	fi
	jq -e --arg i "$3" --arg p "$2" --arg path "$tree" \
		'.item == $i and .placement == $p and .version == "1.0.0" and .path == $path
		and (.tree_hash | test("^sha256:[0-9a-f]{64}$"))
		and (.placed_hash | test("^sha256:[0-9a-f]{64}$")) and '"$hashes" \
		"$rc" >/dev/null || fail "receipt $rc does not match: $(cat "$rc")"
}

# write_desktop_script USER MANAGED writes the Desktop admin fact script:
# assigned_user, which tapas-desktop-managed's .mcp.json.tmpl reads, and
# tapas_desktop_managed, which the manifest's conditional block reads to move
# tapas-desktop-optional into managed_installs. Root-owned, 0755.
write_desktop_script() {
	mkdir -p "$CONDITIONS_DIR"
	printf '#!/bin/sh\necho %s\n' "'{\"assigned_user\": \"$1\", \"tapas_desktop_managed\": \"$2\", \"tapas_shared\": \"admin\"}'" >"$DESKTOP_SCRIPT"
	chown root:wheel "$DESKTOP_SCRIPT"
	chmod 0755 "$DESKTOP_SCRIPT"
}

# receipt_times lists every receipt with its modification time.
receipt_times() {
	find "$RECEIPT_DIR" -type f -name '*.json' | sort | while read -r f; do
		printf '%s %s\n' "$(stat -f %m "$f")" "$f"
	done
}

# assert_skills_index SKILL... checks the templated index of
# claude-skills (its files entry in placements.yaml): JSON with the os
# fact, the placement root and exactly the skills placed there.
assert_skills_index() {
	local want
	[ -f "$SKILLS_INDEX" ] || fail "$SKILLS_INDEX is missing"
	want=$(printf '%s\n' "$@" | sort | jq -R . | jq -sc .)
	jq -e --arg root "$CLAUDE_ROOT" --argjson want "$want" \
		'.os == "darwin" and .root == $root and ([.skills[].name] | sort) == $want' \
		"$SKILLS_INDEX" >/dev/null || fail "skills index does not list $*: $(cat "$SKILLS_INDEX")"
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

# user_setting prints one enabledPlugins value of the user's settings file,
# "absent" when the key is missing.
user_setting() { jq -r --arg k "$1" '.enabledPlugins | if has($k) then (.[$k] | tostring) else "absent" end' "$USER_SETTINGS" 2>/dev/null; }
user_setting_is() { [ "$(user_setting "$1")" = "$2" ]; }

# merge_owns succeeds when the record says sofcat added enabledPlugins.$1.
merge_owns() {
	[ -f "$MERGE_RECORD" ] && jq -e --arg k "$1" \
		'any(.entries[]?; .placement == "tapas-marketplace" and any(.keys[]; . == ["enabledPlugins", $k]))' "$MERGE_RECORD" >/dev/null
}

user_placed() { [ -f "$USER_TREE/SKILL.md" ] && [ -f "$USER_RECEIPT" ]; }

# agent_runs prints how many times launchd has run the console user's helper.
agent_runs() {
	launchctl print "$AGENT_TARGET" 2>/dev/null | awk -F' = ' '/^\truns = / {print $2; exit}'
}

# --- Preconditions and the daemon ---------------------------------------------

[ "$(id -u)" = 0 ] || fail "run as root: sudo $0"
for tool in jq shasum /usr/libexec/PlistBuddy; do
	command -v "$tool" >/dev/null || fail "$tool is missing"
done
[ -x "$SOFCAT" ] || fail "$SOFCAT is missing; install the fixture brand's .pkg first"
[ -f "$CONFIG" ] || fail "$CONFIG is missing; write the smoke config first"
[ -f "$PLIST" ] || fail "$PLIST is missing; install the fixture brand's .pkg first"
[ -f "/Library/LaunchAgents/$AGENT_LABEL.plist" ] || fail "the helper's LaunchAgent plist is missing; install the fixture brand's .pkg first"
[ "$CONSOLE_UID" -ge 500 ] 2>/dev/null || fail "sign a user in to the Mac's GUI first: no loginwindow runs as a user"

step "The Desktop plugins' admin fact script is installed before the daemon restarts"
[ ! -e "$DESKTOP_SCRIPT" ] || fail "$DESKTOP_SCRIPT exists before the smoke wrote it (not a clean Mac?)"
write_desktop_script jdoe no
pass "$DESKTOP_SCRIPT prints assigned_user=jdoe, tapas_desktop_managed=no"

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
assert_skills_index tapas-managed-skill
pass "tapas-managed-skill placed under $CLAUDE_ROOT, receipt managed=true; skills index lists it"

step "The Codex placement is per machine on macOS"
assert_placed "$CODEX_ROOT" codex-skills tapas-managed-skill
jq -e '[.ManagedInstalls[] | select(.name == "tapas-managed-skill" and .status == "installed")] | length == 1' "$INVENTORY" >/dev/null ||
	fail "inventory does not record tapas-managed-skill as installed"
# tapas-user-skill's NOTE.md.tmpl reads .User, which is empty in machine
# scope (the Windows helper renders the login name; run-tapas-user-sync.ps1).
assert_placed "$CODEX_ROOT" codex-skills tapas-user-skill SKILL.md rendered
user_note="$CODEX_ROOT/tapas-user-skill/NOTE.md"
[ -f "$user_note.tmpl" ] || fail "$user_note.tmpl left the tree"
[ "$(cat "$user_note")" = "$(printf 'user: \nmanaged: true\nscope: machine')" ] || fail "NOTE.md reads: $(cat "$user_note")"
jq -e '.managed == true and (.rendered | length) == 1 and .rendered[0].file == "NOTE.md" and .rendered[0].template == "NOTE.md.tmpl"
	and .rendered[0].inputs.user == "" and .rendered[0].inputs.scope == "machine" and .rendered[0].inputs.item.managed == true and .rendered[0].inputs.facts == {}' \
	"$(receipt_path codex-skills tapas-user-skill)" >/dev/null || fail "tapas-user-skill receipt: $(cat "$(receipt_path codex-skills tapas-user-skill)")"
pass "tapas-managed-skill and tapas-user-skill placed under $CODEX_ROOT; NOTE.md rendered with no user, managed=true"

step "The built-in facts are the macOS ones"
jq -e '.Conditions.os == "darwin" and .Conditions.machine_manufacturer == "Apple" and (.Conditions.os_version | type == "string") and (.Conditions | has("os_build") | not)' "$INVENTORY" >/dev/null ||
	fail "inventory Conditions: $(jq -c '.Conditions' "$INVENTORY")"
pass "$(jq -c '.Conditions | {os, os_version, machine_model, machine_type, machine_manufacturer}' "$INVENTORY")"

step "The exclusive placement root sofcat created carries the .$SLUG-owned marker"
assert_placed "$EXCLUSIVE_ROOT" exclusive-skills tapas-exclusive-skill
[ -f "$EXCLUSIVE_ROOT/.$SLUG-owned" ] || fail "marker $EXCLUSIVE_ROOT/.$SLUG-owned is missing"
[ -f "$EXCLUSIVE_INDEX" ] || fail "$EXCLUSIVE_INDEX is missing"
jq -e '.skills == ["tapas-exclusive-skill"]' "$EXCLUSIVE_INDEX" >/dev/null ||
	fail "exclusive index does not list tapas-exclusive-skill: $(cat "$EXCLUSIVE_INDEX")"
pass "tapas-exclusive-skill placed and $EXCLUSIVE_ROOT/.$SLUG-owned present; exclusive index lists it"

step "Held conditional items are not placed while their facts are missing"
[ ! -e "$CONDITION_SCRIPT" ] || fail "$CONDITION_SCRIPT exists before the smoke wrote it (not a clean Mac?)"
assert_not_placed "$CLAUDE_ROOT" claude-skills tapas-scoped-skill
assert_not_placed "$CLAUDE_ROOT" claude-skills tapas-held-skill
for fact in tapas_smoke_scope tapas_smoke_held; do
	warned_held "$fact" || fail "inventory does not warn that the $fact block is held: $(jq -c '.Warnings' "$INVENTORY")"
done
pass "tapas-scoped-skill and tapas-held-skill held, not placed"

# --- The per-user helper ----------------------------------------------------------

step "The daemon staged tapas-user-plugin's archive and lists it for the helper"
sofcat ListUserPlacements
user_entry=$(json_lines | jq -c 'select(.item == "tapas-user-plugin" and .placement == "tapas-user-plugins")' 2>/dev/null | head -n1)
[ -n "$user_entry" ] || fail "ListUserPlacements does not list tapas-user-plugin for tapas-user-plugins"
printf '%s' "$user_entry" | jq -e --arg dir "$USER_ARTIFACTS" \
	'.held == false and .managed == true and .path == "${HOME}/.agents/tapas-plugins" and (.archive | startswith($dir + "/"))' >/dev/null ||
	fail "tapas-user-plugin entry: $user_entry"
archive=$(printf '%s' "$user_entry" | jq -r .archive)
[ "$(sha "$archive")" = "$(printf '%s' "$user_entry" | jq -r .hash)" ] || fail "staged archive $archive does not match its hash"
pass "$archive staged; held=false, managed=true"

step "The daemon started the helper in $CONSOLE_USER's GUI session"
# On a clean Mac the user signed in before the install, so launchd had not
# loaded the LaunchAgent for them: the daemon bootstrapped it, and RunAtLoad
# ran it.
wait_for "$CONSOLE_USER's helper to place tapas-user-plugin" user_placed
launchctl print "$AGENT_TARGET" >/dev/null 2>&1 || fail "$AGENT_TARGET is not loaded"
pass "$USER_TREE placed; $AGENT_TARGET loaded"

# --- Zip-only build ---------------------------------------------------------------

step "The zip-only build skips the managed exe item and logs why"
jq -e '[.ManagedInstalls[] | select(.name == "tapas-exe-item" and .status == "installed")] | length == 0' "$INVENTORY" >/dev/null ||
	fail "inventory records tapas-exe-item as installed"
line=$(assert_logged_rejection tapas-exe-item) || exit 1
printf '%s' "$line" | grep -Eq 'installer\.type .{1,4}exe.{1,4} is not zip' || fail "the rejection does not give the reason: $line"
pass "tapas-exe-item not installed; log: $line"

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
assert_skills_index tapas-managed-skill tapas-optional-skill
pass "tapas-optional-skill placed, status=Installed, in the skills index"

step "RemoveItem removes the optional skill and its receipt"
assert_succeeded RemoveItem:tapas-optional-skill
wait_removed "$CLAUDE_ROOT" claude-skills tapas-optional-skill
wait_pruned
assert_skills_index tapas-managed-skill
pass "tree and receipt gone, managed_uninstalls pruned, skills index back to the managed skill"

# --- Marketplace and settings drop-in -----------------------------------------------

plugin_entries() { jq -c --arg n "$1" '[.plugins[] | select(.name == $n)]' "$MARKETPLACE_JSON"; }
enabled() { jq -e --arg k "$1" '.enabledPlugins[$k] == true' "$DROP_IN" >/dev/null; }

codex_entries() { jq -c --arg n "$1" '[.plugins[] | select(.name == $n)]' "$CODEX_JSON"; }

# assert_codex_entry NAME POLICY RECEIPT checks one Codex catalog entry: a
# local source under plugins/, the installation policy from the managed flag,
# and the version stamped with the receipt's placed hash.
assert_codex_entry() {
	local want
	want="1.0.0+$(jq -r '.placed_hash[7:19]' "$3")"
	codex_entries "$1" | jq -e --arg n "$1" --arg p "$2" --arg v "$want" \
		'length == 1 and .[0].source == {source: "local", path: ("./plugins/" + $n)} and .[0].policy.installation == $p
		and (.[0].version | test("^1\\.0\\.0\\+[0-9a-f]{12}$")) and .[0].version == $v' >/dev/null ||
		fail "$1 Codex entry: $(codex_entries "$1"), want local ./plugins/$1, $2, version $want"
}

step "The startup run places the managed plugin and generates marketplace.json"
assert_placed "$PLUGINS_ROOT" tapas-marketplace tapas-managed-plugin .claude-plugin/plugin.json
[ -f "$MARKET_ROOT/.$SLUG-owned" ] || fail "the marketplace root has no .$SLUG-owned marker"
[ -f "$MARKETPLACE_JSON" ] || fail "$MARKETPLACE_JSON is missing"
jq -e '.name == "tapas-test" and .owner.name == "Tapas Test" and .renames["old-plugin"] == "tapas-managed-plugin" and .allowCrossMarketplaceDependenciesOn == ["other"]' \
	"$MARKETPLACE_JSON" >/dev/null || fail "marketplace.json: $(jq -c . "$MARKETPLACE_JSON")"
plugin_entries tapas-managed-plugin | jq -e 'length == 1 and .[0].source == "./plugins/tapas-managed-plugin" and .[0].version == "1.0.0" and .[0].defaultEnabled == true' >/dev/null ||
	fail "tapas-managed-plugin entry: $(plugin_entries tapas-managed-plugin)"
[ "$(plugin_entries tapas-optional-plugin)" = "[]" ] || fail "the unselected optional plugin is listed"
[ -f "$CODEX_JSON" ] || fail "$CODEX_JSON is missing"
jq -e '.name == "tapas-test" and .interface.displayName == "Tapas Test"' "$CODEX_JSON" >/dev/null || fail "Codex catalog: $(jq -c . "$CODEX_JSON")"
assert_codex_entry tapas-managed-plugin INSTALLED_BY_DEFAULT "$(receipt_path tapas-marketplace tapas-managed-plugin)"
[ "$(codex_entries tapas-optional-plugin)" = "[]" ] || fail "the unselected optional plugin is in the Codex catalog"
pass "plugins/tapas-managed-plugin placed; listed with defaultEnabled=true; renames and allowCrossMarketplaceDependenciesOn as configured; Codex catalog stamped INSTALLED_BY_DEFAULT"

step "The drop-in registers the marketplace and enables the managed plugin"
[ -f "$DROP_IN" ] || fail "$DROP_IN is missing"
jq -e --arg p "$MARKET_ROOT" '.extraKnownMarketplaces["tapas-test"].source == {source: "directory", path: $p}' "$DROP_IN" >/dev/null ||
	fail "extraKnownMarketplaces.tapas-test: $(jq -c '.extraKnownMarketplaces' "$DROP_IN"), want directory $MARKET_ROOT"
enabled tapas-managed-plugin@tapas-test || fail "enabledPlugins lacks tapas-managed-plugin@tapas-test: $(jq -c '.enabledPlugins' "$DROP_IN")"
count=$(find "$(dirname "$DROP_IN")" -mindepth 1 -maxdepth 1 | wc -l | tr -d ' ')
[ "$count" = 1 ] || fail "managed-settings.d holds more than the drop-in: $(ls -A "$(dirname "$DROP_IN")")"
pass "$DROP_IN registers $MARKET_ROOT and enables tapas-managed-plugin@tapas-test"

step "The helper adds the managed plugin to the user's settings; the user's own keys are seeded beside it"
# The daemon's startup run placed the managed plugin before this smoke could
# seed anything, so the seeded false is for tapas-second-plugin, placed below.
# The helper runs in the user's session on the plan change; wait for it.
wait_for "the helper to enable tapas-managed-plugin@tapas-test for $CONSOLE_USER" user_setting_is tapas-managed-plugin@tapas-test true
merge_owns tapas-managed-plugin@tapas-test || fail "the record lacks tapas-managed-plugin@tapas-test: $(cat "$MERGE_RECORD")"
seeded=$(jq '.model = "keep-me" | .enabledPlugins["tapas-second-plugin@tapas-test"] = false' "$USER_SETTINGS") || fail "$USER_SETTINGS is not JSON"
printf '%s\n' "$seeded" >"$USER_SETTINGS"
chown "$CONSOLE_USER" "$USER_SETTINGS"
pass "$USER_SETTINGS: managed plugin true and owned; model=keep-me and tapas-second-plugin@tapas-test=false seeded"

step "InstallItem adds the optional plugin, listed but not enabled"
assert_succeeded InstallItem:tapas-optional-plugin
assert_placed "$PLUGINS_ROOT" tapas-marketplace tapas-optional-plugin .claude-plugin/plugin.json
plugin_entries tapas-optional-plugin | jq -e 'length == 1 and .[0].defaultEnabled == false' >/dev/null ||
	fail "tapas-optional-plugin entry: $(plugin_entries tapas-optional-plugin)"
! enabled tapas-optional-plugin@tapas-test || fail "the optional plugin is in enabledPlugins"
enabled tapas-managed-plugin@tapas-test || fail "the managed plugin left enabledPlugins"
assert_codex_entry tapas-optional-plugin AVAILABLE "$(receipt_path tapas-marketplace tapas-optional-plugin)"
assert_codex_entry tapas-managed-plugin INSTALLED_BY_DEFAULT "$(receipt_path tapas-marketplace tapas-managed-plugin)"
pass "tapas-optional-plugin listed with defaultEnabled=false, not in enabledPlugins; Codex catalog AVAILABLE"

step "The helper adds the optional plugin to the user's settings, past the seeded false"
# tapas-second-plugin goes in first, so the plan that adds the optional key
# already holds the seeded one: add_if_absent must leave it false and unowned.
assert_succeeded InstallItem:tapas-second-plugin
assert_placed "$PLUGINS_ROOT" tapas-marketplace tapas-second-plugin .claude-plugin/plugin.json
assert_succeeded InstallItem:tapas-optional-plugin
wait_for "the helper to enable tapas-optional-plugin@tapas-test for $CONSOLE_USER" user_setting_is tapas-optional-plugin@tapas-test true
merge_owns tapas-optional-plugin@tapas-test || fail "the record lacks tapas-optional-plugin@tapas-test: $(cat "$MERGE_RECORD")"
user_setting_is tapas-second-plugin@tapas-test false || fail "the seeded false changed: $(cat "$USER_SETTINGS")"
! merge_owns tapas-second-plugin@tapas-test || fail "the record lists the seeded key: $(cat "$MERGE_RECORD")"
[ "$(jq -r .model "$USER_SETTINGS")" = keep-me ] || fail "model changed: $(cat "$USER_SETTINGS")"
pass "$USER_SETTINGS: optional plugin true and owned; seeded false and model untouched"

step "RemoveItem drops the optional plugin's entry"
assert_succeeded RemoveItem:tapas-optional-plugin
wait_removed "$PLUGINS_ROOT" tapas-marketplace tapas-optional-plugin
wait_pruned
[ "$(plugin_entries tapas-optional-plugin)" = "[]" ] || fail "tapas-optional-plugin still listed in marketplace.json"
[ "$(plugin_entries tapas-managed-plugin | jq length)" = 1 ] || fail "tapas-managed-plugin no longer listed"
enabled tapas-managed-plugin@tapas-test || fail "the managed plugin left enabledPlugins"
[ "$(codex_entries tapas-optional-plugin)" = "[]" ] || fail "tapas-optional-plugin still in the Codex catalog"
assert_codex_entry tapas-managed-plugin INSTALLED_BY_DEFAULT "$(receipt_path tapas-marketplace tapas-managed-plugin)"
pass "tree, receipt and both catalog entries gone; the managed plugin stays enabled"

step "The helper takes back only the optional plugin's key"
wait_for "the helper to remove tapas-optional-plugin@tapas-test for $CONSOLE_USER" user_setting_is tapas-optional-plugin@tapas-test absent
! merge_owns tapas-optional-plugin@tapas-test || fail "the record still lists the removed key: $(cat "$MERGE_RECORD")"
user_setting_is tapas-second-plugin@tapas-test false || fail "the seeded false changed: $(cat "$USER_SETTINGS")"
[ "$(jq -r .model "$USER_SETTINGS")" = keep-me ] || fail "model changed: $(cat "$USER_SETTINGS")"
assert_succeeded RemoveItem:tapas-second-plugin
wait_removed "$PLUGINS_ROOT" tapas-marketplace tapas-second-plugin
wait_pruned
pass "optional plugin's key gone and unrecorded; seeded false and model still there; tapas-second-plugin removed"

# --- Required software -------------------------------------------------------------

# tapas-depends-skill depends_check Demo Tool, Munki item DemoTool.
step "A skill whose required Munki item is missing is deferred and nothing is written"
operation InstallItem:tapas-depends-skill
if [ "$(terminal .state)" != Deferred ] || [ "$(terminal .errorCode)" != missing_software ]; then
	fail "InstallItem:tapas-depends-skill expected Deferred/missing_software, got $TERMINAL"
fi
want_message="Needs Demo Tool. Install it first, then try again."
terminal .errorMessage | grep -qF "$want_message" || fail "the deferral message is not '$want_message': $(terminal .errorMessage)"
assert_not_placed "$CLAUDE_ROOT" claude-skills tapas-depends-skill
selected tapas-depends-skill || fail "the deferred tapas-depends-skill left the self-serve managed_installs, so no later run retries it"
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
assert_succeeded InstallItem:tapas-depends-skill
assert_placed "$CLAUDE_ROOT" claude-skills tapas-depends-skill
pass "tapas-depends-skill placed with a receipt"

step "RemoveItem removes the skill that needed Demo Tool"
assert_succeeded RemoveItem:tapas-depends-skill
wait_removed "$CLAUDE_ROOT" claude-skills tapas-depends-skill
wait_pruned
rm -rf "$MUNKI_DIR"
pass "tree and receipt gone; $MUNKI_DIR removed"

# --- Adoption --------------------------------------------------------------------

step "A catalog-equal tree placed by hand is adopted without being rewritten"
# Must equal test/manual/fixtures/tapas/item-trees/tapas-adopt-skill/SKILL.md
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

# restart_daemon restarts the daemon, which reads the policy once at startup.
restart_daemon() {
	launchctl kickstart -k "system/$LABEL" >/dev/null || fail "could not restart system/$LABEL"
	wait_for "system/$LABEL to be running" running
	wait_for "the daemon to answer on $SOCKET" answers
}

# The user-scope placement outside the brand's user root, in the console
# user's home, and its receipt.
POLICY_USER_ROOT="$CONSOLE_HOME/.tapas-policy"
POLICY_USER_TREE="$POLICY_USER_ROOT/skills/tapas-policy-user-skill"
POLICY_USER_RECEIPT="$CONSOLE_HOME/Library/Application Support/$PRODUCT/receipts/policy-user-skills/tapas-policy-user-skill.json"
policy_user_placed() { [ -f "$POLICY_USER_TREE/SKILL.md" ] && [ -f "$POLICY_USER_RECEIPT" ]; }
agent_idle() { ! launchctl print "$AGENT_TARGET" 2>/dev/null | grep -q 'state = running'; }

# rerun_helper makes the next run start the console user's helper again, by
# forgetting the plan it was last started for and what it placed, then waits
# for that run of the helper to end.
rerun_helper() {
	rm -f "$DATA_DIR/user-plan.json.started" "$USER_RECEIPT" "$POLICY_USER_RECEIPT"
	rm -rf "$USER_TREE" "$POLICY_USER_ROOT"
	invoke_run
	wait_for "$CONSOLE_USER's helper to place tapas-user-plugin again" user_placed
	wait_for "$AGENT_TARGET to finish" agent_idle
}

step "A write root from managed policy admits it after a restart"
[ ! -e "$POLICY_PLIST" ] || fail "$POLICY_PLIST exists before the smoke wrote it (not a clean Mac?)"
# The helper has run (it placed tapas-user-plugin) and refused this one.
[ ! -e "$POLICY_USER_ROOT" ] || fail "$POLICY_USER_ROOT exists without the policy"
mkdir -p "$(dirname "$POLICY_PLIST")"
# Binary, as MDM writes managed preferences. The user root keeps ${HOME}
# unexpanded, as an administrator would write it.
plutil -create binary1 "$POLICY_PLIST" &&
	plutil -insert WriteRoots -json "{\"Machine\":[\"$OUTSIDE_ROOT\"],\"User\":[\"\${HOME}/.tapas-policy\"]}" "$POLICY_PLIST" &&
	plutil -convert binary1 "$POLICY_PLIST" || fail "could not write $POLICY_PLIST"
chown root:wheel "$POLICY_PLIST" && chmod 644 "$POLICY_PLIST"
restart_daemon
assert_succeeded InstallItem:tapas-outside-skill
assert_placed "$OUTSIDE_ROOT/skills" outside-skills tapas-outside-skill
assert_succeeded RemoveItem:tapas-outside-skill
wait_removed "$OUTSIDE_ROOT/skills" outside-skills tapas-outside-skill
wait_pruned
pass "placed under $OUTSIDE_ROOT/skills with the policy, then removed"

step "The helper places under a user root from managed policy"
rerun_helper
policy_user_placed || fail "$POLICY_USER_TREE or its receipt is missing with the policy's user root"
[ "$(stat -f %u "$POLICY_USER_TREE/SKILL.md")" = "$CONSOLE_UID" ] || fail "$POLICY_USER_TREE is not $CONSOLE_USER's"
pass "$POLICY_USER_TREE placed by $CONSOLE_USER's helper"

step "A policy file others can write is ignored"
chmod 666 "$POLICY_PLIST"
restart_daemon
assert_failed_with InstallItem:tapas-outside-skill placement_refused "outside the write roots"
assert_succeeded RemoveItem:tapas-outside-skill
wait_pruned
rerun_helper
[ ! -e "$POLICY_USER_ROOT" ] || fail "the helper placed under $POLICY_USER_ROOT with $POLICY_PLIST at mode 666"
rm -f "$POLICY_PLIST"
rm -rf "$OUTSIDE_ROOT"
restart_daemon
pass "service and helper refuse with $POLICY_PLIST at mode 666; file removed"

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
assert_skills_index tapas-managed-skill tapas-adopt-skill tapas-scoped-skill tapas-held-skill
pass "tapas-scoped-skill and tapas-held-skill placed and in the skills index"

step "The repository's condition script is synced, and a hand-installed script wins a shared fact"
# The fixture repository delivers conditions/darwin/05-tapas-synced; nothing
# here installed it. It and $DESKTOP_SCRIPT both print tapas_shared.
[ -f "$SYNCED_SCRIPT" ] || fail "$SYNCED_SCRIPT was not synced from the repository; see $LOG_PATH"
perm=$(stat -f '%Su %Lp' "$SYNCED_SCRIPT")
[ "$perm" = "root 755" ] || fail "$SYNCED_SCRIPT is $perm, want root 755"
[ ! -e "$CONDITIONS_DIR/$(basename "$SYNCED_SCRIPT")" ] || fail "the sync wrote into $CONDITIONS_DIR, which belongs to the admin"
synced_facts() { jq -e '.Conditions.tapas_synced == "repository" and .Conditions.tapas_shared == "admin"' "$INVENTORY" >/dev/null; }
wait_for "inventory Conditions with tapas_synced=repository and tapas_shared=admin (now $(jq -c '.Conditions | {tapas_synced, tapas_shared}' "$INVENTORY"))" synced_facts
pass "$SYNCED_SCRIPT is $perm; tapas_synced=repository, tapas_shared=admin"

step "Scope exit removes the item that left the desired set and keeps the held one"
# tapas_smoke_scope turns false; tapas_smoke_held goes missing, so its block is held.
write_condition_script '{"tapas_smoke_scope": "out"}'
invoke_run
wait_removed "$CLAUDE_ROOT" claude-skills tapas-scoped-skill
assert_placed "$CLAUDE_ROOT" claude-skills tapas-held-skill
warned_held tapas_smoke_held || fail "inventory does not warn that the tapas_smoke_held block is held: $(jq -c '.Warnings' "$INVENTORY")"
assert_placed "$CLAUDE_ROOT" claude-skills tapas-managed-skill
# claude-skills never empties: tapas-managed-skill stays, so the index stays
# and follows the receipts. Its removal with the last skill is a unit test.
assert_skills_index tapas-managed-skill tapas-adopt-skill tapas-held-skill
pass "tapas-scoped-skill removed, from the skills index too; tapas-held-skill and tapas-managed-skill kept"

step "A condition script a user could change is skipped"
write_condition_script '{"tapas_smoke_scope": "in", "tapas_smoke_held": "yes"}'
chmod 0777 "$CONDITION_SCRIPT"
invoke_run
assert_not_placed "$CLAUDE_ROOT" claude-skills tapas-scoped-skill
grep -F "skipping file in the conditions directory" "$LOG_PATH" | grep -qF "10-tapas-smoke" ||
	fail "$LOG_PATH does not say the world-writable script was skipped"
pass "world-writable $CONDITION_SCRIPT skipped; tapas-scoped-skill not placed"

# --- Claude Desktop plugins: tree templates --------------------------------------------

DESKTOP_MANAGED_TREE="$DESKTOP_ROOT/tapas-desktop-managed"
DESKTOP_OPTIONAL_TREE="$DESKTOP_ROOT/tapas-desktop-optional"
MANAGED_PLUGIN_JSON="$DESKTOP_MANAGED_TREE/.claude-plugin/plugin.json"
MANAGED_MCP_JSON="$DESKTOP_MANAGED_TREE/.mcp.json"
OPTIONAL_PLUGIN_JSON="$DESKTOP_OPTIONAL_TREE/.claude-plugin/plugin.json"
DESKTOP_MANAGED_RC=$(receipt_path tapas-desktop-plugins tapas-desktop-managed)
DESKTOP_OPTIONAL_RC=$(receipt_path tapas-desktop-plugins tapas-desktop-optional)

# file_times lists every file under $1 with its modification time, to the
# fraction of a second.
file_times() {
	find "$1" -type f | sort | while read -r f; do
		printf '%s %s\n' "$(stat -f %Fm "$f")" "$f"
	done
}

# assert_only_changed BEFORE AFTER FILE... checks that between two file_times
# listings exactly the FILEs were rewritten. fail exits the subshell the
# pipeline runs in, hence the || exit.
assert_only_changed() {
	local before="$1" after="$2"
	shift 2
	[ "$(printf '%s\n' "$before" | wc -l)" = "$(printf '%s\n' "$after" | wc -l)" ] || fail "files appeared or vanished:
$after"
	printf '%s\n' "$before" | while read -r was path; do
		now=$(printf '%s\n' "$after" | awk -v p="$path" 'substr($0, index($0, " ") + 1) == p { print $1 }')
		[ -n "$now" ] || fail "$path was removed"
		case " $* " in
		*" $path "*) [ "$now" != "$was" ] || fail "$path was not re-rendered" ;;
		*) [ "$now" = "$was" ] || fail "$path was rewritten" ;;
		esac
	done || exit 1
}

# receipt_times_except PATH is receipt_times without PATH's line.
receipt_times_except() { receipt_times | grep -vF " $1"; }

step "The Desktop plugins render plugin.json and .mcp.json on the device"
# The daemon's startup run had the Desktop script; installing the optional
# plugin runs everything again either way.
assert_succeeded InstallItem:tapas-desktop-optional
assert_placed "$DESKTOP_ROOT" tapas-desktop-plugins tapas-desktop-managed .claude-plugin/plugin.json rendered
assert_placed "$DESKTOP_ROOT" tapas-desktop-plugins tapas-desktop-optional .claude-plugin/plugin.json rendered
jq -e '.managed == true' "$DESKTOP_MANAGED_RC" >/dev/null || fail "tapas-desktop-managed receipt says managed=false"
jq -e '.managed == false' "$DESKTOP_OPTIONAL_RC" >/dev/null || fail "tapas-desktop-optional receipt says managed=true"
for tmpl in "$MANAGED_PLUGIN_JSON.tmpl" "$MANAGED_MCP_JSON.tmpl" "$OPTIONAL_PLUGIN_JSON.tmpl"; do
	[ -f "$tmpl" ] || fail "template $tmpl left the tree"
done
jq -e '.installationPreference == "auto_install" and .name == "tapas-desktop-managed" and .version == "1.0.0"' "$MANAGED_PLUGIN_JSON" >/dev/null ||
	fail "managed plugin.json: $(cat "$MANAGED_PLUGIN_JSON")"
jq -e '.installationPreference == "available"' "$OPTIONAL_PLUGIN_JSON" >/dev/null || fail "optional plugin.json: $(cat "$OPTIONAL_PLUGIN_JSON")"
jq -e '.mcpServers["tapas-chatproxy"].headers == {"x-litellm-api-key": "Bearer jdoe/claudecode", "x-tapas-platform": "macos"}' "$MANAGED_MCP_JSON" >/dev/null ||
	fail ".mcp.json: $(cat "$MANAGED_MCP_JSON")"
jq -e '(.rendered | length) == 2 and .rendered[0] .file == ".claude-plugin/plugin.json" and .rendered[0].template == ".claude-plugin/plugin.json.tmpl"
	and .rendered[1].file == ".mcp.json" and .rendered[1].template == ".mcp.json.tmpl"
	and .rendered[0].inputs.facts == {}
	and .rendered[1].inputs == {os: "darwin", scope: "machine", user: "", item: {name: "tapas-desktop-managed", version: "1.0.0", managed: true}, facts: {assigned_user: "jdoe", os: "darwin"}}' \
	"$DESKTOP_MANAGED_RC" >/dev/null || fail "tapas-desktop-managed rendered records: $(jq -c .rendered "$DESKTOP_MANAGED_RC")"
jq -e '(.rendered | length) == 1 and .rendered[0].inputs.item.managed == false and .rendered[0].inputs.facts == {}' "$DESKTOP_OPTIONAL_RC" >/dev/null ||
	fail "tapas-desktop-optional rendered records: $(jq -c .rendered "$DESKTOP_OPTIONAL_RC")"
pass "auto_install and available; .mcp.json carries jdoe and macos; two records, .mcp.json read exactly assigned_user and os; placed_hash differs from tree_hash"

step "Moving the optional Desktop plugin into managed_installs re-renders only its plugin.json"
files_before=$(file_times "$DESKTOP_ROOT")
receipts_before=$(receipt_times_except "$DESKTOP_OPTIONAL_RC")
optional_rc_before=$(stat -f %Fm "$DESKTOP_OPTIONAL_RC")
optional_hashes_before=$(jq -c '{tree_hash, placed_hash}' "$DESKTOP_OPTIONAL_RC")
write_desktop_script jdoe yes
invoke_run
assert_only_changed "$files_before" "$(file_times "$DESKTOP_ROOT")" "$OPTIONAL_PLUGIN_JSON"
assert_placed "$DESKTOP_ROOT" tapas-desktop-plugins tapas-desktop-optional .claude-plugin/plugin.json rendered
jq -e --argjson was "$optional_hashes_before" '.managed == true and .tree_hash == $was.tree_hash and .placed_hash != $was.placed_hash' "$DESKTOP_OPTIONAL_RC" >/dev/null ||
	fail "flip: receipt $(jq -c '{managed, tree_hash, placed_hash}' "$DESKTOP_OPTIONAL_RC"), was $optional_hashes_before; want managed, the same tree_hash and a new placed_hash"
jq -e '.installationPreference == "auto_install"' "$OPTIONAL_PLUGIN_JSON" >/dev/null || fail "optional plugin.json after the flip: $(cat "$OPTIONAL_PLUGIN_JSON")"
[ "$(stat -f %Fm "$DESKTOP_OPTIONAL_RC")" != "$optional_rc_before" ] || fail "the flip did not rewrite $DESKTOP_OPTIONAL_RC"
[ "$(receipt_times_except "$DESKTOP_OPTIONAL_RC")" = "$receipts_before" ] || fail "the flip rewrote another receipt"
pass "plugin.json now auto_install, receipt managed=true with the same tree_hash and a new placed_hash; nothing else rewritten"

step "Changing the fact .mcp.json read re-renders it and nothing else"
files_before=$(file_times "$DESKTOP_ROOT")
receipts_before=$(receipt_times_except "$DESKTOP_MANAGED_RC")
managed_hashes_before=$(jq -c '{tree_hash, placed_hash}' "$DESKTOP_MANAGED_RC")
write_desktop_script asmith yes
invoke_run
assert_only_changed "$files_before" "$(file_times "$DESKTOP_ROOT")" "$MANAGED_MCP_JSON"
jq -e '.mcpServers["tapas-chatproxy"].headers["x-litellm-api-key"] == "Bearer asmith/claudecode"' "$MANAGED_MCP_JSON" >/dev/null ||
	fail ".mcp.json after the fact change: $(cat "$MANAGED_MCP_JSON")"
assert_placed "$DESKTOP_ROOT" tapas-desktop-plugins tapas-desktop-managed .claude-plugin/plugin.json rendered
jq -e --argjson was "$managed_hashes_before" '.tree_hash == $was.tree_hash and .placed_hash != $was.placed_hash and .rendered[1].inputs.facts.assigned_user == "asmith"' "$DESKTOP_MANAGED_RC" >/dev/null ||
	fail "fact change: receipt $(jq -c '{tree_hash, placed_hash, rendered}' "$DESKTOP_MANAGED_RC"), was $managed_hashes_before"
[ "$(receipt_times_except "$DESKTOP_MANAGED_RC")" = "$receipts_before" ] || fail "the fact change rewrote another receipt"
pass ".mcp.json re-rendered for asmith; the Desktop plugin.json files and every other receipt untouched"

step "A hand edit to a rendered file is a conflict and the tree is left alone"
mcp_copy=$(mktemp)
cp -p "$MANAGED_MCP_JSON" "$mcp_copy"
echo "edited by the tapas smoke" >>"$MANAGED_MCP_JSON"
edited_hash=$(sha "$MANAGED_MCP_JSON")
rc_before=$(stat -f %Fm "$DESKTOP_MANAGED_RC")
invoke_run
jq -e '[.ManagedInstalls[] | select(.name == "tapas-desktop-managed")] | length == 1 and .[0].status == "failed" and (.[0].error | test("changed outside"))' "$INVENTORY" >/dev/null ||
	fail "inventory does not report the conflict: $(jq -c '[.ManagedInstalls[] | select(.name == "tapas-desktop-managed")]' "$INVENTORY")"
[ "$(sha "$MANAGED_MCP_JSON")" = "$edited_hash" ] || fail "the edited .mcp.json was rewritten"
[ "$(stat -f %Fm "$DESKTOP_MANAGED_RC")" = "$rc_before" ] || fail "the conflicting tree's receipt was rewritten"
# Restored, the next run finds the tree current again.
cat "$mcp_copy" >"$MANAGED_MCP_JSON"
rm -f "$mcp_copy"
invoke_run
jq -e '[.ManagedInstalls[] | select(.name == "tapas-desktop-managed")] | length == 1 and .[0].status == "installed"' "$INVENTORY" >/dev/null ||
	fail "inventory after the restore: $(jq -c '[.ManagedInstalls[] | select(.name == "tapas-desktop-managed")]' "$INVENTORY")"
pass "failed/placement_conflict in the inventory, edited file and receipt kept; installed again once restored"

step "Back in optional_installs, the Desktop plugin renders available again and RemoveItem removes it"
write_desktop_script asmith no
invoke_run
jq -e '.installationPreference == "available"' "$OPTIONAL_PLUGIN_JSON" >/dev/null || fail "optional plugin.json after the flip back: $(cat "$OPTIONAL_PLUGIN_JSON")"
jq -e '.managed == false' "$DESKTOP_OPTIONAL_RC" >/dev/null || fail "the optional Desktop plugin's receipt still says managed=true"
assert_succeeded RemoveItem:tapas-desktop-optional
wait_removed "$DESKTOP_ROOT" tapas-desktop-plugins tapas-desktop-optional
wait_pruned
assert_placed "$DESKTOP_ROOT" tapas-desktop-plugins tapas-desktop-managed .claude-plugin/plugin.json rendered
pass "available again, then tree and receipt gone; tapas-desktop-managed stays"

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

# --- Last item out ----------------------------------------------------------------

step "Removing the last exclusive skill removes its index"
# tapas-exclusive-skill is a default install (tapas_manifest.yaml), so it is
# removable; the root keeps its marker, the index has nothing left to list.
assert_succeeded RemoveItem:tapas-exclusive-skill
wait_removed "$EXCLUSIVE_ROOT" exclusive-skills tapas-exclusive-skill
wait_pruned
assert_not_placed "$EXCLUSIVE_ROOT" exclusive-skills tapas-exclusive-skill
[ ! -e "$EXCLUSIVE_INDEX" ] || fail "$EXCLUSIVE_INDEX still exists with nothing placed"
pass "tree, receipt and $EXCLUSIVE_INDEX gone"

step "A plan the helpers were not given kickstarts the loaded LaunchAgent"
# Forgetting the plan the helpers were last started for makes the next run
# start them again; the agent is loaded now, so that is a kickstart.
runs_before=$(agent_runs)
rm -f "$DATA_DIR/user-plan.json.started" "$USER_RECEIPT"
rm -rf "$USER_TREE"
invoke_run
wait_for "$CONSOLE_USER's helper to place tapas-user-plugin again" user_placed
[ "$(agent_runs)" -gt "$runs_before" ] || fail "$AGENT_TARGET ran $(agent_runs) times, as before"
pass "$USER_TREE placed again; $AGENT_TARGET runs $runs_before -> $(agent_runs)"

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
