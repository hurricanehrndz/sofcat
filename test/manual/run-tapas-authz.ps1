<#
.SYNOPSIS
Per-user authorization check for the Tapas Test fixture brand, one step per run.

.DESCRIPTION
Only the user who selected tapas-optional-skill, or an administrator, may
remove it. Each -Step runs under the caller's own token, so two SSH sessions
drive the check: the rig administrator (whose full token the service sees as
an administrator) and the rig's standard user. test/manual/run-tapas-authz.sh
runs the steps in this order from the host, after run-tapas-smoke.ps1:

  admin  prepare          service up, no selector, config copied for the standard user
  admin  select           InstallItem succeeds; selected_by is the admin's SID
  std    remove-refused   RemoveItem exits non-zero with not_authorized
  admin  verify-unchanged selection, selected_by and the placed tree as before
  admin  remove           RemoveItem succeeds; tree, receipt and selector gone
  std    select           InstallItem succeeds
  admin  verify-selector  selected_by is the standard user's SID (-Selector)
  admin  remove-other     the administrator removes the standard user's item
  std    select           InstallItem succeeds
  std    remove-own       the selector removes its own item
  admin  verify-clean     nothing placed, no selector; the Public copy removed

Standard-user steps read the config copy under C:\Users\Public, because
config.yaml and service-manifest.yaml are readable by SYSTEM and
Administrators only, so only administrator steps assert on the manifest.
`select` prints `AUTHZ CALLER <sid>`; the driver passes the standard user's
SID to verify-selector as -Selector.

Each step exits non-zero on its first failed assertion and prints
`AUTHZ STEP <name> PASSED` at its end. Every line also goes to -LogPath.
PowerShell 5.1 compatible.
#>

[CmdletBinding()]
param(
    # Not Mandatory: a prompt for it would hang an SSH session.
    [ValidateSet('prepare', 'select', 'remove-refused', 'verify-unchanged', 'remove',
        'verify-selector', 'remove-other', 'remove-own', 'verify-clean')]
    [string]$Step        = "",
    # The SID verify-selector expects as the item's selector (from the
    # standard user's `select` output). Defaults to the caller's own SID.
    [string]$Selector    = "",
    [string]$SofCat      = "$env:ProgramFiles\Tapas Test\tapas-test.exe",
    [string]$Config      = "$env:ProgramData\Tapas Test\config.yaml",
    # The copy of the config a standard user can read; only its pipe name matters.
    [string]$UserConfig  = "C:\Users\Public\tapas-authz-config.yaml",
    # The data directory (app_data_path), beside config.yaml.
    [string]$DataDir     = "$env:ProgramData\Tapas Test\Data",
    [string]$Slug        = "tapas-test",
    # The brand's write root; see test/manual/fixtures/tapas/brand/harnesses.yaml.
    [string]$SmokeRoot   = "$env:ProgramData\tapas-test-smoke",
    [string]$LogPath     = "C:\sofcat-test\tapas-authz.log",
    [int]$TimeoutSec     = 120
)

$ErrorActionPreference = "Stop"

$receiptDir = Join-Path $DataDir "receipts"
$SelfServe  = Join-Path $DataDir "service-manifest.yaml"
$claudeRoot = Join-Path $SmokeRoot "claude\skills"
$optionalItem = "tapas-optional-skill"

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

function Pass {
    param([string]$Message)
    Log "    $Message"
}

# Invoke-SofCat runs a service command. Returns @{Code=<int>; Out=<string[]>}.
# By default a non-zero exit is a hard failure; pass -AllowFail to inspect it.
function Invoke-SofCat {
    param([string]$Spec, [switch]$AllowFail)
    # 2>&1 turns native stderr lines into ErrorRecords; with EAP=Stop that
    # becomes a terminating NativeCommandError. Relax EAP around the call so
    # stderr flows into $out as strings and we still check $LASTEXITCODE.
    $savedEAP = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $out = & $SofCat -c $Config -S $Spec 2>&1
    } finally {
        $ErrorActionPreference = $savedEAP
    }
    $code = $LASTEXITCODE
    $lines = @($out | ForEach-Object { "$_" })
    Log ("    {0} -S {1} (exit {2})" -f $Slug, $Spec, $code)
    $lines | ForEach-Object { Log "      $_" }
    if (-not $AllowFail -and $code -ne 0) {
        Fail "$Slug -S $Spec exited $code"
    }
    return @{ Code = $code; Out = $lines }
}

# Get-YamlList parses a top-level YAML sequence value ("key:\n  - a\n  - b").
# Good enough for the flat self-serve manifest; returns @() when absent.
function Get-YamlList {
    param([string]$Path, [string]$Key)
    if (-not (Test-Path -LiteralPath $Path)) { return @() }
    $items = @()
    $inKey = $false
    foreach ($raw in Get-Content -LiteralPath $Path) {
        if ($raw -match '^\s*#') { continue }
        if ($raw -match "^${Key}:\s*(.*)$") {
            $inKey = $true
            $inline = $Matches[1].Trim()
            if ($inline -and $inline -ne '[]') {
                $inline.Trim('[',']').Split(',') | ForEach-Object {
                    $v = $_.Trim(); if ($v) { $items += $v }
                }
                $inKey = $false
            }
            continue
        }
        if ($inKey) {
            if ($raw -match '^\s*-\s*(.+?)\s*$') {
                $items += $Matches[1].Trim()
            } elseif ($raw -match '^\S') {
                $inKey = $false
            }
        }
    }
    return @($items)
}

# Get-YamlMap parses a top-level YAML mapping of scalars ("key:\n  a: b").
# Good enough for the self-serve selected_by; returns @{} when absent.
function Get-YamlMap {
    param([string]$Path, [string]$Key)
    $map = @{}
    if (-not (Test-Path -LiteralPath $Path)) { return $map }
    $inKey = $false
    foreach ($raw in Get-Content -LiteralPath $Path) {
        if ($raw -match '^\s*#') { continue }
        if ($raw -match "^${Key}:\s*(.*)$") {
            $inKey = ($Matches[1].Trim() -eq '')
            continue
        }
        if ($inKey) {
            if ($raw -match '^\s+([^:\s]+):\s*(.+?)\s*$') {
                $map[$Matches[1]] = $Matches[2].Trim('"', "'")
            } elseif ($raw -match '^\S') {
                $inKey = $false
            }
        }
    }
    return $map
}

function Wait-For {
    param([ScriptBlock]$Condition, [string]$Description)
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        if (& $Condition) { return }
        Start-Sleep -Seconds 3
    }
    Fail "timed out after ${TimeoutSec}s waiting for: $Description"
}

# Invoke-Operation sends an InstallItem/RemoveItem command, streams the
# operation to its end, and returns the terminal status record.
function Invoke-Operation {
    param([string]$Spec)
    return (Wait-Operation (Invoke-SofCat $Spec) $Spec)
}

# Wait-Operation streams the operation an accepted InstallItem/RemoveItem
# result names to its end and returns the terminal status record.
function Wait-Operation {
    param($Res, [string]$Spec)
    $opId = $null
    foreach ($l in $Res.Out) {
        if ("$l" -match 'operationId:\s*(\S+)') { $opId = $Matches[1]; break }
    }
    if (-not $opId) { Fail "no operationId returned for $Spec" }
    $stream = Invoke-SofCat "StreamOperationStatus:$opId"
    $terminal = $null
    foreach ($l in $stream.Out) {
        $t = "$l".Trim()
        if (-not $t.StartsWith('{')) { continue }
        try { $ev = $t | ConvertFrom-Json } catch { continue }
        if ($ev.operationId -eq $opId -and @('Succeeded','Failed','Deferred','Canceled') -contains $ev.state) {
            $terminal = $ev
        }
    }
    if (-not $terminal) { Fail "no terminal event received for $Spec" }
    return $terminal
}

function Assert-Succeeded {
    param([string]$Spec)
    $terminal = Invoke-Operation $Spec
    if ($terminal.state -ne "Succeeded") {
        Fail "$Spec ended $($terminal.state): $($terminal | ConvertTo-Json -Compress)"
    }
}

function Receipt-Path {
    param([string]$Placement, [string]$Item)
    return (Join-Path (Join-Path $receiptDir $Placement) "$Item.json")
}

# Assert-Placed checks a tree and its receipt.
function Assert-Placed {
    param([string]$Root, [string]$Placement, [string]$Item, [string]$File = "SKILL.md")
    $tree = Join-Path $Root $Item
    if (-not (Test-Path -LiteralPath (Join-Path $tree $File))) { Fail "$tree\$File is missing" }
    $rcPath = Receipt-Path $Placement $Item
    if (-not (Test-Path -LiteralPath $rcPath)) { Fail "receipt $rcPath is missing" }
    $rc = Get-Content -LiteralPath $rcPath -Raw | ConvertFrom-Json
    if ($rc.item -ne $Item -or $rc.placement -ne $Placement -or $rc.version -ne "1.0.0") {
        Fail "receipt $rcPath does not match: $($rc | ConvertTo-Json -Compress)"
    }
    if ($rc.path -ne $tree) { Fail "receipt path '$($rc.path)' is not $tree" }
    if ($rc.tree_hash -notmatch '^sha256:[0-9a-f]{64}$') { Fail "receipt tree_hash '$($rc.tree_hash)'" }
    return $rc
}

function Wait-Removed {
    param([string]$Root, [string]$Placement, [string]$Item)
    $tree = Join-Path $Root $Item
    $rcPath = Receipt-Path $Placement $Item
    Wait-For { -not (Test-Path -LiteralPath $tree) -and -not (Test-Path -LiteralPath $rcPath) } "$Item to leave $Placement with its receipt"
}

function Wait-Pruned {
    Wait-For { @(Get-YamlList $SelfServe "managed_uninstalls").Count -eq 0 } "managed_uninstalls to be pruned empty"
}

function Get-Selector {
    return (Get-YamlMap $SelfServe "selected_by")[$optionalItem]
}

# Assert-Selected checks the item is selected, placed and recorded to $Sid.
function Assert-Selected {
    param([string]$Sid)
    if (@(Get-YamlList $SelfServe "managed_installs") -notcontains $optionalItem -or
        @(Get-YamlList $SelfServe "managed_uninstalls") -contains $optionalItem) {
        Fail "$optionalItem is not selected"
    }
    $selector = Get-Selector
    if ($selector -ne $Sid) { Fail "selected_by.$optionalItem = '$selector', want $Sid" }
    Assert-Placed $claudeRoot "claude-skills" $optionalItem | Out-Null
}

# Assert-Removed waits for the tree, receipt and removal entry to go, then
# checks the selector was cleared with them.
function Assert-Removed {
    Wait-Removed $claudeRoot "claude-skills" $optionalItem
    Wait-Pruned
    if ((Get-YamlMap $SelfServe "selected_by").ContainsKey($optionalItem)) { Fail "selected_by still records $optionalItem after its removal completed" }
}

try {
    if (-not $Step) { Fail "usage: run-tapas-authz.ps1 -Step <name> [-Selector <sid>]" }
    New-Item -ItemType Directory -Path (Split-Path $LogPath) -Force -ErrorAction SilentlyContinue | Out-Null
    if ($Step -eq 'prepare') { Set-Content -LiteralPath $LogPath -Value "" -ErrorAction SilentlyContinue }

    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $sid = $identity.User.Value
    $isAdmin = (New-Object Security.Principal.WindowsPrincipal $identity).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    Log ("[{0}] running as {1} ({2})" -f $Step, $identity.Name, $(if ($isAdmin) { "administrator" } else { "standard user" }))
    Log "AUTHZ CALLER $sid"
    # `select` runs as either; the others belong to one caller, and running
    # them as the other would pass or fail for the wrong reason.
    $stdSteps = @('remove-refused', 'remove-own')
    if ($Step -ne 'select') {
        if ($isAdmin -and $stdSteps -contains $Step) { Fail "step $Step must run as a standard user, not an administrator" }
        if (-not $isAdmin -and $stdSteps -notcontains $Step) { Fail "step $Step needs an administrator token" }
    }
    if (-not (Test-Path -LiteralPath $SofCat -PathType Leaf)) { Fail "$SofCat is not installed" }
    if (-not $isAdmin) {
        if (-not (Test-Path -LiteralPath $UserConfig -PathType Leaf)) { Fail "$UserConfig is missing: run the prepare step first" }
        $Config = $UserConfig
    }
    if (-not $Selector) { $Selector = $sid }

    switch ($Step) {
        'prepare' {
            Invoke-SofCat "GetServiceInfo" | Out-Null
            if ((Get-YamlMap $SelfServe "selected_by").ContainsKey($optionalItem)) { Fail "$optionalItem has a recorded selector before the check" }
            if (Test-Path -LiteralPath (Join-Path $claudeRoot $optionalItem)) { Fail "$optionalItem is placed before the check" }
            # config.yaml is locked to SYSTEM and Administrators, so a standard
            # user cannot read the service's. The client only needs the pipe
            # name from it: give the user a copy under Public. Public's own ACL
            # admits interactive logons only, and the standard user's SSH
            # session is a network logon, so grant Users read explicitly.
            Copy-Item -LiteralPath $Config -Destination $UserConfig -Force
            icacls $UserConfig /grant '*S-1-5-32-545:R' | Out-Null
            if ($LASTEXITCODE -ne 0) { Fail "icacls could not grant Users read on $UserConfig" }
            Pass "service up, $optionalItem unselected; config copied to $UserConfig"
        }
        'select' {
            Assert-Succeeded "InstallItem:$optionalItem"
            if ($isAdmin) {
                Assert-Selected $sid
                Pass "installed $optionalItem; selected_by records $sid"
            } else {
                Pass "installed $optionalItem as a standard user"
            }
        }
        'remove-refused' {
            $res = Invoke-SofCat "RemoveItem:$optionalItem" -AllowFail
            $text = $res.Out -join "`n"
            if ($res.Code -eq 0) { Fail "RemoveItem:$optionalItem by another standard user was accepted" }
            if ($text -notmatch 'not_authorized') { Fail "RemoveItem was not refused with not_authorized: $text" }
            Pass "RemoveItem by another standard user refused with not_authorized"
        }
        'verify-unchanged' {
            Assert-Selected $Selector
            Pass "the refused removal changed nothing: still selected by $Selector and placed"
        }
        'remove' {
            Assert-Succeeded "RemoveItem:$optionalItem"
            Assert-Removed
            Pass "the administrator's RemoveItem succeeded; tree and receipt gone, selector cleared"
        }
        'verify-selector' {
            if ($Selector -eq $sid) { Fail "-Selector is this administrator's own SID; pass the standard user's" }
            Assert-Selected $Selector
            Pass "selected_by records the standard user $Selector"
        }
        'remove-other' {
            Assert-Succeeded "RemoveItem:$optionalItem"
            Assert-Removed
            Pass "the administrator removed the standard user's item; selector cleared"
        }
        'remove-own' {
            Assert-Succeeded "RemoveItem:$optionalItem"
            Pass "the standard user removed its own item"
        }
        'verify-clean' {
            Assert-Removed
            Remove-Item -LiteralPath $UserConfig -Force -ErrorAction SilentlyContinue
            Pass "nothing placed, no selector; $UserConfig removed"
        }
    }

    Log "AUTHZ STEP $Step PASSED"
    exit 0
}
catch {
    Fail $_.Exception.Message
}
