<#
.SYNOPSIS
User-scope placement check for the Tapas Test fixture brand.

.DESCRIPTION
Runs the per-user helper, `tapas-test-useragent.exe`, and asserts that
it placed the Codex skills in this user's profile: the trees under
%USERPROFILE%\.agents\skills, their receipts under
%LOCALAPPDATA%\Tapas Test\receipts with the plan's managed flag,
tapas-user-skill's NOTE.md rendered with this account's login name, and
nothing under %USERPROFILE%\.agents owned by SYSTEM (the service must never
write there). It also checks the placement spec reaches the helper:
tapas-user-plugin lands in the exclusive tapas-user-plugins root, which the
helper marks as its own, under its items_dir, beside an index.json rendered
with this account's login name, the placement's vars and its items; once
deleted, that file is back after a second run.

Run AFTER run-tapas-smoke.ps1 has passed (a service run has staged the
archive and stored the plan), in the logged-on user's desktop session with a
standard, non-elevated token, for example with `rig run-it --wait`.
Desktop-session output is otherwise invisible, so every line also goes to
-LogPath. Create C:\sofcat-test first if a standard user cannot.

Exits non-zero on the first failed assertion; prints TAPAS USER SYNC PASSED
at the end. PowerShell 5.1 compatible.
#>

[CmdletBinding()]
param(
    [string]$Agent       = "$env:ProgramFiles\Tapas Test\tapas-test-useragent.exe",
    [string]$ProductName = "Tapas Test",
    [string]$Item        = "tapas-managed-skill",
    # The managed skill whose NOTE.md.tmpl the helper renders with this
    # account's login name and the plan's managed flag.
    [string]$UserItem    = "tapas-user-skill",
    [string]$Placement   = "codex-skills",
    # The managed item in the exclusive, items_dir and files placement.
    [string]$PluginItem      = "tapas-user-plugin",
    [string]$PluginPlacement = "tapas-user-plugins",
    [string]$Slug        = "tapas-test",
    [string]$Version     = "1.0.0",
    [string]$LogPath     = "C:\sofcat-test\tapas-user-sync.log",
    [int]$TimeoutSec     = 120
)

$ErrorActionPreference = "Stop"

function Log {
    param([string]$Message)
    Write-Host $Message
    try { Add-Content -LiteralPath $LogPath -Value ("{0:o} {1}" -f (Get-Date), $Message) } catch { }
}

function Fail {
    param([string]$Message)
    Log "ASSERTION FAILED: $Message"
    exit 1
}

# Invoke-UserSync runs the helper once and fails unless it exits 0.
function Invoke-UserSync {
    Log "Starting $Agent"
    $process = Start-Process -FilePath $Agent -PassThru
    # Caching the handle keeps ExitCode readable after exit in PowerShell 5.1.
    $null = $process.Handle
    if (-not $process.WaitForExit($TimeoutSec * 1000)) {
        $process | Stop-Process -Force
        Fail "the agent did not exit within ${TimeoutSec}s"
    }
    if ($process.ExitCode -ne 0) { Fail "the agent exited $($process.ExitCode)" }
    Log "the agent exited 0"
}

try {
    New-Item -ItemType Directory -Path (Split-Path $LogPath) -Force -ErrorAction SilentlyContinue | Out-Null
    Set-Content -LiteralPath $LogPath -Value "" -ErrorAction SilentlyContinue

    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    Log "Running as $($identity.Name)"
    if ((New-Object Security.Principal.WindowsPrincipal $identity).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Fail "this check needs a standard (non-elevated) token"
    }
    if (-not (Test-Path -LiteralPath $Agent -PathType Leaf)) { Fail "$Agent is not installed" }

    # Start without the tree and its receipt, so a pass proves this run of
    # the helper placed them.
    $agents = Join-Path $env:USERPROFILE ".agents"
    $tree = Join-Path $agents "skills\$Item"
    $receiptPath = Join-Path $env:LOCALAPPDATA "$ProductName\receipts\$Placement\$Item.json"
    $userTree = Join-Path $agents "skills\$UserItem"
    $userReceiptPath = Join-Path $env:LOCALAPPDATA "$ProductName\receipts\$Placement\$UserItem.json"
    # The whole plugin root goes, marker included, so the helper must create
    # and mark it again for the exclusive placement to accept it.
    $pluginRoot = Join-Path $agents "tapas-plugins"
    $pluginReceiptPath = Join-Path $env:LOCALAPPDATA "$ProductName\receipts\$PluginPlacement\$PluginItem.json"
    foreach ($stale in @($tree, $receiptPath, $userTree, $userReceiptPath, $pluginRoot, $pluginReceiptPath)) {
        if (Test-Path -LiteralPath $stale) {
            Remove-Item -LiteralPath $stale -Recurse -Force
            Log "Removed pre-existing $stale"
        }
    }

    # The helper logs to %LOCALAPPDATA%\<slug>\ui-client.log with this set.
    $env:SOFCAT_UI_DEBUG = "1"
    Invoke-UserSync

    if (-not (Test-Path -LiteralPath (Join-Path $tree "SKILL.md"))) { Fail "$tree\SKILL.md is missing" }
    Log "Placed $tree"

    if (-not (Test-Path -LiteralPath $receiptPath)) { Fail "receipt $receiptPath is missing" }
    $rc = Get-Content -LiteralPath $receiptPath -Raw | ConvertFrom-Json
    if ($rc.item -ne $Item -or $rc.placement -ne $Placement -or $rc.version -ne $Version) {
        Fail "receipt $receiptPath does not match: $($rc | ConvertTo-Json -Compress)"
    }
    if ($rc.path -ne $tree) { Fail "receipt path '$($rc.path)' is not $tree" }
    if ($rc.tree_hash -notmatch '^sha256:[0-9a-f]{64}$') { Fail "receipt tree_hash '$($rc.tree_hash)'" }
    if ($rc.placed_hash -notmatch '^sha256:[0-9a-f]{64}$') { Fail "receipt placed_hash '$($rc.placed_hash)'" }
    # tapas-managed-skill ships no template, so it is placed as shipped.
    if ($rc.placed_hash -ne $rc.tree_hash) { Fail "receipt placed_hash '$($rc.placed_hash)' differs from tree_hash '$($rc.tree_hash)'" }
    if (-not $rc.PSObject.Properties['rendered']) { Fail "receipt $receiptPath has no rendered list" }
    if (@($rc.rendered).Count -ne 0) { Fail "receipt rendered is not empty: $($rc.rendered | ConvertTo-Json -Compress)" }
    if (-not $rc.installed_at_utc) { Fail "receipt has no installed_at_utc" }
    if (-not $rc.managed) { Fail "receipt $receiptPath says managed=false; the plan lists $Item in managed_installs" }
    Log "Receipt $receiptPath matches, managed=true"

    # tapas-user-skill ships NOTE.md.tmpl: the helper renders it beside the
    # template with this account's login name (no domain), the plan's managed
    # flag and the scope, and records the render in the receipt.
    $note = Join-Path $userTree "NOTE.md"
    if (-not (Test-Path -LiteralPath $note)) { Fail "$note is missing" }
    if (-not (Test-Path -LiteralPath "$note.tmpl")) { Fail "$note.tmpl left the tree" }
    $noteText = Get-Content -LiteralPath $note -Raw
    $wantNote = "user: $env:USERNAME`nmanaged: true`nscope: user`n"
    if ($noteText -ne $wantNote) { Fail "NOTE.md reads '$noteText', want '$wantNote'" }
    if (-not (Test-Path -LiteralPath $userReceiptPath)) { Fail "receipt $userReceiptPath is missing" }
    $urc = Get-Content -LiteralPath $userReceiptPath -Raw | ConvertFrom-Json
    if ($urc.item -ne $UserItem -or $urc.path -ne $userTree -or -not $urc.managed) { Fail "receipt $userReceiptPath does not match: $($urc | ConvertTo-Json -Compress)" }
    if ($urc.placed_hash -notmatch '^sha256:[0-9a-f]{64}$' -or $urc.placed_hash -eq $urc.tree_hash) { Fail "receipt placed_hash '$($urc.placed_hash)' should differ from tree_hash '$($urc.tree_hash)'" }
    $recs = @($urc.rendered)
    if ($recs.Count -ne 1 -or $recs[0].file -ne "NOTE.md" -or $recs[0].template -ne "NOTE.md.tmpl") { Fail "receipt rendered: $($urc.rendered | ConvertTo-Json -Compress -Depth 5)" }
    $in = $recs[0].inputs
    if ($in.user -ne $env:USERNAME -or $in.scope -ne "user" -or $in.os -ne "windows" -or -not $in.item.managed -or @($in.facts.PSObject.Properties).Count -ne 0) {
        Fail "rendered inputs: $($in | ConvertTo-Json -Compress -Depth 5)"
    }
    Log "$note names $env:USERNAME; receipt $userReceiptPath managed=true with one rendered record"

    # tapas-user-plugins is exclusive with items_dir plugins and a generated
    # index.json: without its spec the helper would place the tree at the
    # root, write no marker and no file.
    $marker = Join-Path $pluginRoot ".$Slug-owned"
    if (-not (Test-Path -LiteralPath $marker)) { Fail "marker $marker is missing: the exclusive root is not the helper's" }
    $pluginTree = Join-Path $pluginRoot "plugins\$PluginItem"
    if (-not (Test-Path -LiteralPath (Join-Path $pluginTree "SKILL.md"))) { Fail "$pluginTree\SKILL.md is missing" }
    if (Test-Path -LiteralPath (Join-Path $pluginRoot $PluginItem)) { Fail "$PluginItem was placed at the root, not under items_dir" }
    if (-not (Test-Path -LiteralPath $pluginReceiptPath)) { Fail "receipt $pluginReceiptPath is missing" }
    $prc = Get-Content -LiteralPath $pluginReceiptPath -Raw | ConvertFrom-Json
    if ($prc.item -ne $PluginItem -or $prc.placement -ne $PluginPlacement -or $prc.path -ne $pluginTree -or -not $prc.managed) {
        Fail "receipt $pluginReceiptPath does not match: $($prc | ConvertTo-Json -Compress)"
    }
    Log "Placed $pluginTree under the marked root $pluginRoot; receipt matches"

    # index.json renders .User, .Placement.Vars.name, .Placement.Root and
    # the placement's items (placements.yaml).
    $index = Join-Path $pluginRoot "index.json"
    if (-not (Test-Path -LiteralPath $index)) { Fail "$index is missing" }
    $indexText = Get-Content -LiteralPath $index -Raw
    $ix = $indexText | ConvertFrom-Json
    $plugins = @($ix.plugins)
    if ($ix.user -ne $env:USERNAME -or $ix.name -ne "tapas-user" -or $ix.root -ne $pluginRoot -or $plugins.Count -ne 1 -or $plugins[0] -ne $PluginItem) {
        Fail "$index reads '$indexText', want user $env:USERNAME, name tapas-user, root $pluginRoot, plugins [$PluginItem]"
    }
    Log "$index names $env:USERNAME, tapas-user and $PluginItem"

    # A sync with no tree change still regenerates the placement's files.
    Remove-Item -LiteralPath $index -Force
    Log "Deleted $index"
    Invoke-UserSync
    if (-not (Test-Path -LiteralPath $index)) { Fail "$index was not restored by a second run" }
    $restored = Get-Content -LiteralPath $index -Raw
    if ($restored -ne $indexText) { Fail "$index restored as '$restored', want '$indexText'" }
    Log "$index restored as it was"

    $paths = @($agents) + @(Get-ChildItem -LiteralPath $agents -Recurse -Force | ForEach-Object { $_.FullName })
    foreach ($path in $paths) {
        $owner = (Get-Acl -LiteralPath $path).Owner
        if ($owner -eq "NT AUTHORITY\SYSTEM") { Fail "$path is owned by SYSTEM" }
    }
    Log "None of the $($paths.Count) entries under $agents is owned by SYSTEM"

    Log "TAPAS USER SYNC PASSED"
    exit 0
}
catch {
    Fail $_.Exception.Message
}
