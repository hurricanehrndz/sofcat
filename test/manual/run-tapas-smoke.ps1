<#
.SYNOPSIS
Zip-item end-to-end smoke test for the Tapas Test fixture brand.

.DESCRIPTION
Drives the running Tapas Test service over its named pipe (via
`tapas-test.exe -S ...`) against the repository `just bootstrap-tapas` lays
out from test/manual/fixtures/tapas, and asserts on the placed trees and
receipts: managed and self-service placement, removal, adoption of a
hand-placed tree, conflict reporting for a user edit, the exclusive-root
marker and refusal, a placement outside the write roots, the skipped
user-scope placement and its staged archive in the user-sync plan, a held
conditional item, and scope exit. run-tapas-user-sync.ps1 then places the
user-scope skill as the logged-on user.

Run once on a clean VM, AFTER bootstrap-vm.ps1 has installed and started the
branded service:
  bootstrap-vm.ps1 -BaseUrl <url> -Slug tapas-test -ProductName "Tapas Test"
    -Manifest tapas_manifest -Catalogs tapas -InstallService -StartService -NoPause

Exits non-zero on the first failed assertion; prints each step and
TAPAS SMOKE PASSED at the end. PowerShell 5.1 compatible.
#>

[CmdletBinding()]
param(
    [string]$SofCat      = "$env:ProgramFiles\Tapas Test\tapas-test.exe",
    [string]$Config      = "$env:ProgramData\Tapas Test\config.yaml",
    [string]$Slug        = "tapas-test",
    # The brand's write root and a sibling outside it; see
    # test/manual/fixtures/tapas/brand/write-roots.yaml and harnesses.yaml.
    [string]$SmokeRoot   = "$env:ProgramData\tapas-test-smoke",
    [string]$OutsideRoot = "$env:ProgramData\tapas-test-outside",
    [int]$TimeoutSec     = 120
)

$ErrorActionPreference = "Stop"
$stepNum = 0

# The data directory holds config.yaml, the receipts, inventory.json, the
# self-service manifest and the admin conditions folder.
$dataDir       = Split-Path $Config
$receiptDir    = Join-Path $dataDir "receipts"
$inventoryPath = Join-Path $dataDir "inventory.json"
$SelfServe     = Join-Path $dataDir "service-manifest.yaml"
$conditionsDir = Join-Path $dataDir "conditions"
$conditionScript = Join-Path $conditionsDir "10-tapas-smoke.ps1"

# Placement roots from harnesses.yaml.
$claudeRoot    = Join-Path $SmokeRoot "claude\skills"
$exclusiveRoot = Join-Path $SmokeRoot "exclusive\skills"
$foreignRoot   = Join-Path $SmokeRoot "foreign\skills"
$outsideRoot   = Join-Path $OutsideRoot "skills"

function Write-Step {
    param([string]$Message)
    $script:stepNum++
    Write-Host ("[{0}] {1}" -f $script:stepNum, $Message) -ForegroundColor Cyan
}

function Fail {
    param([string]$Message)
    Write-Host "ASSERTION FAILED: $Message" -ForegroundColor Red
    exit 1
}

function Pass {
    param([string]$Message)
    Write-Host "    $Message" -ForegroundColor Green
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
    Write-Host ("    {0} -S {1} (exit {2})" -f $Slug, $Spec, $code) -ForegroundColor DarkGray
    $lines | ForEach-Object { Write-Host "      $_" -ForegroundColor DarkGray }
    if (-not $AllowFail -and $code -ne 0) {
        Fail "$Slug -S $Spec exited $code"
    }
    return @{ Code = $code; Out = $lines }
}

# Get-YamlList parses a top-level YAML sequence value ("key:\n  - a\n  - b").
# Good enough for the flat self-serve manifest; returns @() when absent.
function Get-YamlList {
    param([string]$Path, [string]$Key)
    if (-not (Test-Path $Path)) { return @() }
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

function Wait-For {
    param([ScriptBlock]$Condition, [string]$Description)
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        if (& $Condition) { return }
        Start-Sleep -Seconds 3
    }
    Fail "timed out after ${TimeoutSec}s waiting for: $Description"
}

# Get-OptionalItem returns the parsed JSON object for one item from
# ListOptionalInstalls (the client prints one compact JSON object per item).
function Get-OptionalItem {
    param([string]$Name)
    $res = Invoke-SofCat "ListOptionalInstalls"
    foreach ($l in $res.Out) {
        $t = "$l".Trim()
        if (-not $t.StartsWith('{')) { continue }
        try { $obj = $t | ConvertFrom-Json } catch { continue }
        if ($obj.itemName -eq $Name) { return $obj }
    }
    return $null
}

# Invoke-Operation sends an InstallItem/RemoveItem command, streams the
# operation to its end, and returns the terminal status record.
function Invoke-Operation {
    param([string]$Spec)
    $res = Invoke-SofCat $Spec
    $opId = $null
    foreach ($l in $res.Out) {
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

function Assert-FailedWith {
    param([string]$Spec, [string]$Code, [string]$Expect)
    $terminal = Invoke-Operation $Spec
    if ($terminal.state -ne "Failed" -or $terminal.errorCode -ne $Code) {
        Fail "$Spec expected Failed/$Code, got $($terminal | ConvertTo-Json -Compress)"
    }
    if ($terminal.errorMessage -notmatch [regex]::Escape($Expect)) {
        Fail "$Spec error does not mention ${Expect}: $($terminal.errorMessage)"
    }
    return $terminal
}

# Invoke-Run triggers a full run with an idempotent request (the adopted
# skill stays installed from the adoption step on) and waits for it to end. A
# run refreshes the admin facts and reconciles every item.
function Invoke-Run {
    Assert-Succeeded "InstallItem:tapas-adopt-skill"
}

function Receipt-Path {
    param([string]$Placement, [string]$Item)
    return (Join-Path (Join-Path $receiptDir $Placement) "$Item.json")
}

function Read-Inventory {
    return (Get-Content -LiteralPath $inventoryPath -Raw | ConvertFrom-Json)
}

# Assert-Placed checks a tree and its receipt.
function Assert-Placed {
    param([string]$Root, [string]$Placement, [string]$Item)
    $tree = Join-Path $Root $Item
    if (-not (Test-Path (Join-Path $tree "SKILL.md"))) { Fail "$tree\SKILL.md is missing" }
    $rcPath = Receipt-Path $Placement $Item
    if (-not (Test-Path $rcPath)) { Fail "receipt $rcPath is missing" }
    $rc = Get-Content -LiteralPath $rcPath -Raw | ConvertFrom-Json
    if ($rc.item -ne $Item -or $rc.placement -ne $Placement -or $rc.version -ne "1.0.0") {
        Fail "receipt $rcPath does not match: $($rc | ConvertTo-Json -Compress)"
    }
    if ($rc.path -ne $tree) { Fail "receipt path '$($rc.path)' is not $tree" }
    if ($rc.tree_hash -notmatch '^sha256:[0-9a-f]{64}$') { Fail "receipt tree_hash '$($rc.tree_hash)'" }
    return $rc
}

function Assert-NotPlaced {
    param([string]$Root, [string]$Placement, [string]$Item)
    $tree = Join-Path $Root $Item
    if (Test-Path $tree) { Fail "$tree exists" }
    $rcPath = Receipt-Path $Placement $Item
    if (Test-Path $rcPath) { Fail "receipt $rcPath exists" }
}

function Wait-Removed {
    param([string]$Root, [string]$Placement, [string]$Item)
    $tree = Join-Path $Root $Item
    $rcPath = Receipt-Path $Placement $Item
    Wait-For { -not (Test-Path $tree) -and -not (Test-Path $rcPath) } "$Item to leave $Placement with its receipt"
}

function Wait-Pruned {
    Wait-For { (Get-YamlList $SelfServe "managed_uninstalls").Count -eq 0 } "managed_uninstalls to be pruned empty"
}

# --- The startup run ------------------------------------------------------------

Write-Step "The startup run places the managed skill with a receipt"
$managedRc = Receipt-Path "claude-skills" "tapas-managed-skill"
Wait-For { (Test-Path $managedRc) -and (Test-Path $inventoryPath) } "the managed skill's receipt and inventory.json"
$rc = Assert-Placed $claudeRoot "claude-skills" "tapas-managed-skill"
if (-not $rc.managed) { Fail "the managed skill's receipt has managed=false" }
Pass "tapas-managed-skill placed under $claudeRoot, receipt managed=true"

Write-Step "The user-scope Codex placement is skipped and the item still succeeds"
$codexReceipts = Join-Path $receiptDir "codex-skills"
if (Test-Path $codexReceipts) { Fail "the service wrote user-scope receipts under $codexReceipts" }
$systemProfile = Join-Path $env:windir "System32\config\systemprofile"
foreach ($profileDir in @($systemProfile, $env:USERPROFILE)) {
    $codexTree = Join-Path $profileDir ".agents\skills\tapas-managed-skill"
    if (Test-Path $codexTree) { Fail "the service wrote the user-scope tree $codexTree" }
}
$entry = @((Read-Inventory).ManagedInstalls | Where-Object { $_.name -eq "tapas-managed-skill" })
if ($entry.Count -ne 1 -or $entry[0].status -ne "installed") {
    Fail "inventory does not record tapas-managed-skill as installed: $($entry | ConvertTo-Json -Compress)"
}
Pass "no codex-skills tree or receipt; tapas-managed-skill installed"

Write-Step "The exclusive placement root sofcat created carries the .$Slug-owned marker"
Assert-Placed $exclusiveRoot "exclusive-skills" "tapas-exclusive-skill" | Out-Null
$marker = Join-Path $exclusiveRoot ".$Slug-owned"
if (-not (Test-Path $marker)) { Fail "marker $marker is missing" }
Pass "tapas-exclusive-skill placed and $marker present"

Write-Step "Held conditional items are not placed while their facts are missing"
if (Test-Path $conditionScript) { Fail "$conditionScript exists before the smoke wrote it (not a clean VM?)" }
Assert-NotPlaced $claudeRoot "claude-skills" "tapas-scoped-skill"
Assert-NotPlaced $claudeRoot "claude-skills" "tapas-held-skill"
$warnings = @((Read-Inventory).Warnings)
foreach ($fact in @('tapas_smoke_scope', 'tapas_smoke_held')) {
    if (@($warnings | Where-Object { $_ -match $fact -and $_ -match 'held' }).Count -eq 0) {
        Fail "inventory does not warn that the $fact block is held: $($warnings -join '; ')"
    }
}
Pass "tapas-scoped-skill and tapas-held-skill held, not placed"

Write-Step "The service staged the Codex archive and lists it for the user helper"
$userArtifacts = Join-Path (Split-Path $SofCat) "user-artifacts"
$codex = $null
foreach ($l in (Invoke-SofCat "ListUserPlacements").Out) {
    $t = "$l".Trim()
    if (-not $t.StartsWith('{')) { continue }
    try { $obj = $t | ConvertFrom-Json } catch { continue }
    if ($obj.item -eq "tapas-managed-skill" -and $obj.placement -eq "codex-skills") { $codex = $obj }
}
if (-not $codex) { Fail "ListUserPlacements does not list tapas-managed-skill for codex-skills" }
if ($codex.held) { Fail "tapas-managed-skill is listed held: $($codex | ConvertTo-Json -Compress)" }
if ((Split-Path $codex.archive) -ne $userArtifacts) { Fail "archive '$($codex.archive)' is not under $userArtifacts" }
if (-not (Test-Path -LiteralPath $codex.archive)) { Fail "staged archive $($codex.archive) is missing" }
$stagedHash = (Get-FileHash -LiteralPath $codex.archive -Algorithm SHA256).Hash.ToLower()
if ($stagedHash -ne $codex.hash) { Fail "staged archive hash $stagedHash is not $($codex.hash)" }
Pass "$($codex.archive) staged; plan lists tapas-managed-skill/codex-skills, held=false"

# --- Self-service ---------------------------------------------------------------

$optionalTree = Join-Path $claudeRoot "tapas-optional-skill"

Write-Step "ListOptionalInstalls offers the optional skill with a real status"
$opt = Get-OptionalItem "tapas-optional-skill"
if (-not $opt) { Fail "tapas-optional-skill not listed by ListOptionalInstalls" }
if ($opt.status -ne "NotInstalled") { Fail "tapas-optional-skill status '$($opt.status)', expected NotInstalled" }
Pass "tapas-optional-skill offered, status=$($opt.status)"

Write-Step "InstallItem places the optional skill with a receipt"
Assert-Succeeded "InstallItem:tapas-optional-skill"
Assert-Placed $claudeRoot "claude-skills" "tapas-optional-skill" | Out-Null
$opt = Get-OptionalItem "tapas-optional-skill"
if ($opt.status -ne "Installed") { Fail "tapas-optional-skill status '$($opt.status)' after install, expected Installed" }
Pass "tapas-optional-skill placed, status=Installed"

Write-Step "RemoveItem removes the optional skill and its receipt"
Assert-Succeeded "RemoveItem:tapas-optional-skill"
Wait-Removed $claudeRoot "claude-skills" "tapas-optional-skill"
Wait-Pruned
Pass "tree and receipt gone, managed_uninstalls pruned"

# --- Adoption --------------------------------------------------------------------

Write-Step "A catalog-equal tree placed by hand is adopted without being rewritten"
# Must equal test/manual/fixtures/tapas/rendered/tapas-adopt-skill/claude-skills/windows/SKILL.md byte for byte.
$adoptText = "---`nname: tapas-adopt-skill`ndescription: A self-service skill placed by hand and adopted`n---`n"
$adoptTree = Join-Path $claudeRoot "tapas-adopt-skill"
$adoptFile = Join-Path $adoptTree "SKILL.md"
New-Item -ItemType Directory -Path $adoptTree -Force | Out-Null
[IO.File]::WriteAllText($adoptFile, $adoptText, (New-Object Text.UTF8Encoding $false))
$beforeHash = (Get-FileHash -LiteralPath $adoptFile -Algorithm SHA256).Hash
$beforeTime = (Get-Item -LiteralPath $adoptFile).LastWriteTimeUtc
if (Test-Path (Receipt-Path "claude-skills" "tapas-adopt-skill")) { Fail "the hand-placed tree already has a receipt" }
Assert-Succeeded "InstallItem:tapas-adopt-skill"
Assert-Placed $claudeRoot "claude-skills" "tapas-adopt-skill" | Out-Null
if ((Get-FileHash -LiteralPath $adoptFile -Algorithm SHA256).Hash -ne $beforeHash -or
    (Get-Item -LiteralPath $adoptFile).LastWriteTimeUtc -ne $beforeTime) {
    Fail "the adopted tree was rewritten"
}
Pass "receipt written, hand-placed tree untouched"

# --- Conflict ---------------------------------------------------------------------

Write-Step "A user edit fails the removal with placement_conflict and the tree stays"
Assert-Succeeded "InstallItem:tapas-optional-skill"
Assert-Placed $claudeRoot "claude-skills" "tapas-optional-skill" | Out-Null
$optionalFile = Join-Path $optionalTree "SKILL.md"
$originalBytes = [IO.File]::ReadAllBytes($optionalFile)
Add-Content -LiteralPath $optionalFile -Value "edited by the tapas smoke"
$editedHash = (Get-FileHash -LiteralPath $optionalFile -Algorithm SHA256).Hash
Assert-FailedWith "RemoveItem:tapas-optional-skill" "placement_conflict" "claude-skills" | Out-Null
if (-not (Test-Path $optionalFile) -or (Get-FileHash -LiteralPath $optionalFile -Algorithm SHA256).Hash -ne $editedHash) {
    Fail "the edited tree was changed or removed"
}
if (-not (Test-Path (Receipt-Path "claude-skills" "tapas-optional-skill"))) { Fail "the conflicting tree's receipt was removed" }
Pass "Failed/placement_conflict; edited tree and receipt kept"

Write-Step "Restoring the file lets the next run finish the removal"
[IO.File]::WriteAllBytes($optionalFile, $originalBytes)
Invoke-Run
Wait-Removed $claudeRoot "claude-skills" "tapas-optional-skill"
Wait-Pruned
Pass "tree and receipt gone after the restore"

# --- Refusals ----------------------------------------------------------------------

Write-Step "An exclusive root with files sofcat did not put there is refused"
New-Item -ItemType Directory -Path $foreignRoot -Force | Out-Null
Set-Content -LiteralPath (Join-Path $foreignRoot "someone-else.txt") -Value "not sofcat's" -Encoding ASCII
Assert-FailedWith "InstallItem:tapas-foreign-skill" "placement_refused" "foreign-skills" | Out-Null
Assert-NotPlaced $foreignRoot "foreign-skills" "tapas-foreign-skill"
if (Test-Path (Join-Path $foreignRoot ".$Slug-owned")) { Fail "sofcat marked a root it does not own" }
Assert-Succeeded "RemoveItem:tapas-foreign-skill"
Wait-Pruned
Pass "Failed/placement_refused; nothing written, no marker"

Write-Step "A placement outside the write roots is refused"
Assert-FailedWith "InstallItem:tapas-outside-skill" "placement_refused" "outside the write roots" | Out-Null
if (Test-Path $OutsideRoot) { Fail "$OutsideRoot was created" }
Assert-Succeeded "RemoveItem:tapas-outside-skill"
Wait-Pruned
Pass "Failed/placement_refused; $OutsideRoot not created"

# --- Scope exit -------------------------------------------------------------------

Write-Step "An admin fact brings the conditional items into scope"
New-Item -ItemType Directory -Path $conditionsDir -Force | Out-Null
Set-Content -LiteralPath $conditionScript -Encoding ASCII -Value `
    '@{ tapas_smoke_scope = "in"; tapas_smoke_held = "yes" } | ConvertTo-Json -Compress'
Invoke-Run
Assert-Placed $claudeRoot "claude-skills" "tapas-scoped-skill" | Out-Null
Assert-Placed $claudeRoot "claude-skills" "tapas-held-skill" | Out-Null
Pass "tapas-scoped-skill and tapas-held-skill placed"

Write-Step "Scope exit removes the item that left the desired set and keeps the held one"
# tapas_smoke_scope turns false; tapas_smoke_held goes missing, so its block is held.
Set-Content -LiteralPath $conditionScript -Encoding ASCII -Value `
    '@{ tapas_smoke_scope = "out" } | ConvertTo-Json -Compress'
Invoke-Run
Wait-Removed $claudeRoot "claude-skills" "tapas-scoped-skill"
Assert-Placed $claudeRoot "claude-skills" "tapas-held-skill" | Out-Null
$warnings = @((Read-Inventory).Warnings)
if (@($warnings | Where-Object { $_ -match 'tapas_smoke_held' -and $_ -match 'held' }).Count -eq 0) {
    Fail "inventory does not warn that the tapas_smoke_held block is held: $($warnings -join '; ')"
}
Assert-Placed $claudeRoot "claude-skills" "tapas-managed-skill" | Out-Null
Pass "tapas-scoped-skill removed; tapas-held-skill and tapas-managed-skill kept"

Write-Host ""
Write-Host "TAPAS SMOKE PASSED" -ForegroundColor Green
exit 0
