<#
.SYNOPSIS
Per-user authorization check for the Tapas Test fixture brand.

.DESCRIPTION
Creates two local standard users. User A selects tapas-optional-skill through
`tapas-test.exe -S InstallItem:...`, user B's RemoveItem is refused with
not_authorized and changes nothing, then this elevated session removes it as
an administrator. Removes the two users itself.

Starting a process as another user (Start-Process -Credential) fails with
0xC0000142 in an SSH session, so run this AFTER run-tapas-smoke.ps1 has
passed, elevated in the logged-on user's desktop session, for example with
`rig run-it --elevated --wait test/manual/run-tapas-authz.ps1`.
Desktop-session output is otherwise invisible, so every line also goes to
-LogPath.

Exits non-zero on the first failed assertion; prints TAPAS AUTHZ PASSED at
the end. PowerShell 5.1 compatible.
#>

[CmdletBinding()]
param(
    [string]$SofCat      = "$env:ProgramFiles\Tapas Test\tapas-test.exe",
    [string]$Config      = "$env:ProgramData\Tapas Test\config.yaml",
    [string]$Slug        = "tapas-test",
    # The brand's write root; see test/manual/fixtures/tapas/brand/harnesses.yaml.
    [string]$SmokeRoot   = "$env:ProgramData\tapas-test-smoke",
    [string]$LogPath     = "C:\sofcat-test\tapas-authz.log",
    [int]$TimeoutSec     = 120
)

$ErrorActionPreference = "Stop"
$stepNum = 0

$dataDir    = Split-Path $Config
$receiptDir = Join-Path $dataDir "receipts"
$SelfServe  = Join-Path $dataDir "service-manifest.yaml"
$claudeRoot = Join-Path $SmokeRoot "claude\skills"

function Log {
    param([string]$Message)
    Write-Host $Message
    try { Add-Content -LiteralPath $LogPath -Value ("{0:o} {1}" -f (Get-Date), $Message) } catch { }
}

function Write-Step {
    param([string]$Message)
    $script:stepNum++
    Log ("[{0}] {1}" -f $script:stepNum, $Message)
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

# Invoke-SofCatAs runs a service command as another local user, with that
# user's own standard token, and returns @{Code=<int>; Out=<string[]>}.
function Invoke-SofCatAs {
    param([pscredential]$Credential, [string]$Spec)
    # The data directory is locked to SYSTEM and Administrators, so a standard
    # user cannot read the service's config.yaml. The client only needs the
    # pipe name from it: give the user a copy under Public, which anyone reads.
    $userConfig = "C:\Users\Public\tapas-authz-config.yaml"
    if (-not (Test-Path -LiteralPath $userConfig)) { Copy-Item -LiteralPath $Config -Destination $userConfig }
    $base = Join-Path $env:TEMP ("tapas-authz-" + [guid]::NewGuid().ToString('N'))
    $outFile = "$base.out"
    $errFile = "$base.err"
    # Start-Process joins the arguments with spaces, so quote the config path.
    $proc = Start-Process -FilePath $SofCat -ArgumentList @('-c', "`"$userConfig`"", '-S', $Spec) `
        -Credential $Credential -LoadUserProfile -WorkingDirectory (Split-Path $SofCat) `
        -NoNewWindow -Wait -PassThru -RedirectStandardOutput $outFile -RedirectStandardError $errFile
    $lines = @(@(Get-Content -LiteralPath $outFile -ErrorAction SilentlyContinue) +
        @(Get-Content -LiteralPath $errFile -ErrorAction SilentlyContinue) | ForEach-Object { "$_" })
    Remove-Item -LiteralPath $outFile, $errFile -Force -ErrorAction SilentlyContinue
    $code = $proc.ExitCode
    Log ("    {0} -S {1} as {2} (exit {3})" -f $Slug, $Spec, $Credential.UserName, $code)
    $lines | ForEach-Object { Log "      $_" }
    if ($null -eq $code) { Fail "no exit code from $Slug -S $Spec as $($Credential.UserName)" }
    if ($code -eq -1073741502) { Fail "$Slug could not start as $($Credential.UserName) (0xC0000142): this session cannot launch a process for another user" }
    return @{ Code = $code; Out = $lines }
}

# New-SmokeUser creates a local standard user (member of Users only) with a
# generated password and returns its credential. The password is never printed.
function New-SmokeUser {
    param([string]$Name)
    $plain = "Tp1!" + [guid]::NewGuid().ToString('N') + "aZ"
    $secure = ConvertTo-SecureString $plain -AsPlainText -Force
    $plain = $null
    New-LocalUser -Name $Name -Password $secure -AccountNeverExpires -PasswordNeverExpires `
        -Description "tapas smoke: per-user authorization" | Out-Null
    try {
        Add-LocalGroupMember -SID "S-1-5-32-545" -Member $Name -ErrorAction Stop
    } catch [Microsoft.PowerShell.Commands.MemberExistsException] {
        # New-LocalUser already made it a member of Users.
    }
    return (New-Object System.Management.Automation.PSCredential ("$env:COMPUTERNAME\$Name", $secure))
}

function Remove-SmokeUser {
    param([string]$Name)
    $u = Get-LocalUser -Name $Name -ErrorAction SilentlyContinue
    if (-not $u) { return }
    Get-CimInstance Win32_UserProfile | Where-Object { $_.SID -eq $u.SID.Value } |
        Remove-CimInstance -ErrorAction SilentlyContinue
    Remove-LocalUser -Name $Name
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

$userA = "tapas-smoke-a"
$userB = "tapas-smoke-b"
$optionalItem = "tapas-optional-skill"

try {
    New-Item -ItemType Directory -Path (Split-Path $LogPath) -Force -ErrorAction SilentlyContinue | Out-Null
    Set-Content -LiteralPath $LogPath -Value "" -ErrorAction SilentlyContinue

    # Creating users needs an administrator, and this session's RemoveItem is
    # the administrator's removal under test.
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    Log "Running as $($identity.Name)"
    if (-not (New-Object Security.Principal.WindowsPrincipal $identity).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Fail "this check needs an elevated administrator token"
    }
    if (-not (Test-Path -LiteralPath $SofCat -PathType Leaf)) { Fail "$SofCat is not installed" }

    Write-Step "Two standard users: only the user who selected an item, or an admin, removes it"
    foreach ($n in @($userA, $userB)) {
        if (Get-LocalUser -Name $n -ErrorAction SilentlyContinue) { Fail "local user $n exists before the check created it (not a clean VM?)" }
    }
    if ((Get-YamlMap $SelfServe "selected_by").ContainsKey($optionalItem)) { Fail "$optionalItem has a recorded selector before the check" }
    try {
        $credA = New-SmokeUser $userA
        $credB = New-SmokeUser $userB
        $sidA = (Get-LocalUser -Name $userA).SID.Value

        $resA = Invoke-SofCatAs $credA "InstallItem:$optionalItem"
        if ($resA.Code -ne 0) { Fail "InstallItem:$optionalItem as $userA exited $($resA.Code)" }
        $terminal = Wait-Operation $resA "InstallItem:$optionalItem"
        if ($terminal.state -ne "Succeeded") { Fail "$userA's install ended $($terminal.state): $($terminal | ConvertTo-Json -Compress)" }
        Assert-Placed $claudeRoot "claude-skills" $optionalItem | Out-Null
        $selector = (Get-YamlMap $SelfServe "selected_by")[$optionalItem]
        if ($selector -ne $sidA) { Fail "selected_by.$optionalItem = '$selector', want $userA's SID $sidA" }
        Pass "$userA installed $optionalItem; selected_by records $sidA"

        $resB = Invoke-SofCatAs $credB "RemoveItem:$optionalItem"
        $textB = $resB.Out -join "`n"
        if ($resB.Code -eq 0) { Fail "RemoveItem:$optionalItem as $userB was accepted" }
        if ($textB -notmatch 'not_authorized') { Fail "RemoveItem as $userB was not refused with not_authorized: $textB" }
        if (@(Get-YamlList $SelfServe "managed_installs") -notcontains $optionalItem -or
            @(Get-YamlList $SelfServe "managed_uninstalls") -contains $optionalItem) {
            Fail "the refused removal changed the selection"
        }
        if ((Get-YamlMap $SelfServe "selected_by")[$optionalItem] -ne $sidA) { Fail "the refused removal changed selected_by" }
        Assert-Placed $claudeRoot "claude-skills" $optionalItem | Out-Null
        Pass "$userB's RemoveItem refused with not_authorized; still selected and placed"

        Assert-Succeeded "RemoveItem:$optionalItem"
        Wait-Removed $claudeRoot "claude-skills" $optionalItem
        Wait-Pruned
        if ((Get-YamlMap $SelfServe "selected_by").ContainsKey($optionalItem)) { Fail "selected_by still records $optionalItem after its removal completed" }
        Pass "the administrator's RemoveItem succeeded; tree and receipt gone, selector cleared"
    } finally {
        foreach ($n in @($userA, $userB)) { Remove-SmokeUser $n }
    }
    foreach ($n in @($userA, $userB)) {
        if (Get-LocalUser -Name $n -ErrorAction SilentlyContinue) { Fail "local user $n was not removed" }
    }

    Log ""
    Log "TAPAS AUTHZ PASSED"
    exit 0
}
catch {
    Fail $_.Exception.Message
}
