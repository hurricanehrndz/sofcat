<#
.SYNOPSIS
User-scope placement check for the Tapas Test fixture brand.

.DESCRIPTION
Runs the per-user helper, `tapas-test-ui.exe --user-sync`, and asserts that
it placed the Codex skill in this user's profile: the tree under
%USERPROFILE%\.agents\skills, its receipt under
%LOCALAPPDATA%\Tapas Test\receipts, and nothing under %USERPROFILE%\.agents
owned by SYSTEM (the service must never write there).

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
    [string]$UI          = "$env:ProgramFiles\Tapas Test\tapas-test-ui.exe",
    [string]$ProductName = "Tapas Test",
    [string]$Item        = "tapas-managed-skill",
    [string]$Placement   = "codex-skills",
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

try {
    New-Item -ItemType Directory -Path (Split-Path $LogPath) -Force -ErrorAction SilentlyContinue | Out-Null
    Set-Content -LiteralPath $LogPath -Value "" -ErrorAction SilentlyContinue

    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    Log "Running as $($identity.Name)"
    if ((New-Object Security.Principal.WindowsPrincipal $identity).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Fail "this check needs a standard (non-elevated) token"
    }
    if (-not (Test-Path -LiteralPath $UI -PathType Leaf)) { Fail "$UI is not installed" }

    # Start without the tree and its receipt, so a pass proves this run of
    # the helper placed them.
    $agents = Join-Path $env:USERPROFILE ".agents"
    $tree = Join-Path $agents "skills\$Item"
    $receiptPath = Join-Path $env:LOCALAPPDATA "$ProductName\receipts\$Placement\$Item.json"
    foreach ($stale in @($tree, $receiptPath)) {
        if (Test-Path -LiteralPath $stale) {
            Remove-Item -LiteralPath $stale -Recurse -Force
            Log "Removed pre-existing $stale"
        }
    }

    # The helper logs to %LOCALAPPDATA%\<slug>\ui-client.log with this set.
    $env:SOFCAT_UI_DEBUG = "1"
    Log "Starting $UI --user-sync"
    $process = Start-Process -FilePath $UI -ArgumentList "--user-sync" -PassThru
    # Caching the handle keeps ExitCode readable after exit in PowerShell 5.1.
    $null = $process.Handle
    if (-not $process.WaitForExit($TimeoutSec * 1000)) {
        $process | Stop-Process -Force
        Fail "--user-sync did not exit within ${TimeoutSec}s"
    }
    if ($process.ExitCode -ne 0) { Fail "--user-sync exited $($process.ExitCode)" }
    Log "--user-sync exited 0"

    if (-not (Test-Path -LiteralPath (Join-Path $tree "SKILL.md"))) { Fail "$tree\SKILL.md is missing" }
    Log "Placed $tree"

    if (-not (Test-Path -LiteralPath $receiptPath)) { Fail "receipt $receiptPath is missing" }
    $rc = Get-Content -LiteralPath $receiptPath -Raw | ConvertFrom-Json
    if ($rc.item -ne $Item -or $rc.placement -ne $Placement -or $rc.version -ne $Version) {
        Fail "receipt $receiptPath does not match: $($rc | ConvertTo-Json -Compress)"
    }
    if ($rc.path -ne $tree) { Fail "receipt path '$($rc.path)' is not $tree" }
    if ($rc.tree_hash -notmatch '^sha256:[0-9a-f]{64}$') { Fail "receipt tree_hash '$($rc.tree_hash)'" }
    if (-not $rc.installed_at_utc) { Fail "receipt has no installed_at_utc" }
    Log "Receipt $receiptPath matches"

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
