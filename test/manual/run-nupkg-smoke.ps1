<#
.SYNOPSIS
Nupkg-item end-to-end smoke test: embala plain-style packages under sofcat as
SYSTEM, on a machine with no Chocolatey.

.DESCRIPTION
Runs `sofcat.exe -config <cfg>` as SYSTEM, through a temporary scheduled
task, against the repository `just bootstrap-nupkg` lays out, once for each
of its manifests:

  nupkg_install    sofcat-nupkg-smoke 1.0.0 (embala scripts = "generate")
  nupkg_update     sofcat-nupkg-smoke 2.0.0
  nupkg_uninstall  sofcat-nupkg-smoke 2.0.0
  nupkg_bad        sofcat-nupkg-bad 1.0.0, whose install script calls an
                   unknown command

and asserts on what embala's generated scripts leave behind (the install
directory and its HKLM\SOFTWARE\embala\nupkg\<identifier> marker), on
sofcat's kept copy of the package under <DataDir>\nupkg\<id>, and that
%ProgramData%\chocolatey never appears. The bad package must fail at the
unknown command and leave no kept copy and no staging directory. A last,
informational step runs the bad install script with plain -File and logs
whether that would also have failed it.

Run on a clean VM as an administrator (rig ps), with the repository served
at -BaseUrl and sofcat.exe copied to -SofCat. Every parameter has a default,
so the script runs with no arguments.

Exits non-zero on the first failed assertion; prints each step and
NUPKG SMOKE PASSED at the end. Every line also goes to -LogPath. The
scheduled task is removed at the end, on failure too. PowerShell 5.1
compatible.
#>

[CmdletBinding()]
param(
    [string]$SofCat     = "C:\rig\sofcat.exe",
    [string]$BaseUrl    = "http://127.0.0.1:8080/",
    # app_data_path in the config the script writes; the kept copies live in
    # its nupkg directory. This is the stock default.
    [string]$DataDir    = "$env:ProgramData\SofCat\Data",
    [string]$WorkDir    = "C:\sofcat-test\nupkg-smoke",
    [string]$LogPath    = "C:\sofcat-test\nupkg-smoke.log",
    [int]$TimeoutSec    = 300
)

$ErrorActionPreference = "Stop"
$stepNum = 0

$taskName = "rig-sofcat-nupkg-smoke"
$runCmd   = Join-Path $WorkDir "run.cmd"
$runOut   = Join-Path $WorkDir "run-out.txt"
$runCode  = Join-Path $WorkDir "run-code.txt"
$sofcatCopy = Join-Path $WorkDir "sofcat.exe"

$chocoRoot  = Join-Path $env:ProgramData "chocolatey"
$keptRoot   = Join-Path $DataDir "nupkg"
$smokeId    = "sofcat-nupkg-smoke"
$badId      = "sofcat-nupkg-bad"
$smokeKept  = Join-Path $keptRoot $smokeId
$badKept    = Join-Path $keptRoot $badId
# embala's generated scripts: <ProgramFiles>\<display-name> (nupkg), recorded
# under HKLM\SOFTWARE\embala\nupkg\<identifier> (build-nupkg-repo.sh).
$programFiles = $env:ProgramW6432
if (-not $programFiles) { $programFiles = $env:ProgramFiles }
$smokeDir   = Join-Path $programFiles "SofCat Nupkg Smoke (nupkg)"
$smokeKey   = "SOFTWARE\embala\nupkg\ca.hrndz.sofcat.$smokeId"
$badKey     = "SOFTWARE\embala\nupkg\ca.hrndz.sofcat.$badId"
# bad-install.ps1 writes this only if it runs past the unknown command.
$badContinued = Join-Path $env:ProgramData "sofcat-nupkg-bad-continued.txt"

$hklm = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::LocalMachine, [Microsoft.Win32.RegistryView]::Registry64)

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

# Fail throws, so the finally below still removes the task.
function Fail {
    param([string]$Message)
    throw "ASSERTION FAILED: $Message"
}

function Pass {
    param([string]$Message)
    Log "    $Message"
}

function Test-Exists {
    param([string]$Path)
    return ([System.IO.Directory]::Exists($Path) -or [System.IO.File]::Exists($Path))
}

function Assert-Exists {
    param([string]$Path)
    if (-not (Test-Exists $Path)) { Fail "$Path does not exist" }
    Pass "$Path exists"
}

function Assert-Missing {
    param([string]$Path)
    if (Test-Exists $Path) { Fail "$Path exists" }
    Pass "no $Path"
}

function Assert-NoChocolatey {
    if (Test-Exists $chocoRoot) { Fail "$chocoRoot exists: this smoke needs a machine without Chocolatey" }
    Pass "no $chocoRoot"
}

# Get-MarkerDir returns the InstallDir value under an embala marker key, or
# $null when the key does not exist.
function Get-MarkerDir {
    param([string]$Key)
    $k = $hklm.OpenSubKey($Key)
    if ($null -eq $k) { return $null }
    try { return [string]$k.GetValue("InstallDir") } finally { $k.Close() }
}

function Assert-MarkerMissing {
    param([string]$Key)
    if ($null -ne $hklm.OpenSubKey($Key)) { Fail "HKLM\$Key exists" }
    Pass "no HKLM\$Key"
}

# Get-KeptVersion reads the version from the kept copy's .nuspec.
function Get-KeptVersion {
    param([string]$Kept)
    $nuspecs = @(Get-ChildItem -LiteralPath $Kept -Filter "*.nuspec" -File -ErrorAction SilentlyContinue)
    if ($nuspecs.Count -ne 1) { Fail "$Kept holds $($nuspecs.Count) .nuspec files, want 1" }
    [xml]$doc = Get-Content -LiteralPath $nuspecs[0].FullName -Raw
    return [string]$doc.package.metadata.version
}

function Assert-NoStaging {
    param([string]$Id)
    Assert-Missing (Join-Path $keptRoot ".staging-$Id")
    Assert-Missing (Join-Path $keptRoot ".old-$Id")
}

function Assert-SmokeInstalled {
    param([string]$Version)
    $dir = Get-MarkerDir $smokeKey
    if ($null -eq $dir) { Fail "HKLM\$smokeKey does not exist" }
    if ($dir.TrimEnd('\') -ne $smokeDir) { Fail "HKLM\$smokeKey InstallDir is '$dir', want '$smokeDir'" }
    Pass "HKLM\$smokeKey InstallDir is $dir"
    $readme = Join-Path $smokeDir "readme.txt"
    Assert-Exists $readme
    $payload = (Get-Content -LiteralPath $readme -Raw).Trim()
    if ($payload -ne "$smokeId $Version") { Fail "$readme says '$payload', want '$smokeId $Version'" }
    Pass "$readme says '$payload'"
    $kept = Get-KeptVersion $smokeKept
    if ($kept -ne $Version) { Fail "kept copy $smokeKept holds version '$kept', want '$Version'" }
    Pass "kept copy $smokeKept holds version $kept"
    Assert-NoStaging $smokeId
}

function Write-Config {
    param([string]$Manifest)
    $path = Join-Path $WorkDir "$Manifest.yaml"
    $yamlDataDir = $DataDir -replace "\\", "/"
    @"
url: $BaseUrl
url_packages: $BaseUrl
manifest: $Manifest
catalogs:
  - nupkg
app_data_path: $yamlDataDir
"@ | Set-Content -LiteralPath $path -Encoding ASCII
    return $path
}

# Invoke-SofCatAsSystem runs sofcat for one manifest through the scheduled
# task and returns @{Code=<int>; Out=<string[]>}. The task runs run.cmd, which
# records sofcat's output and exit code in files the script waits for.
function Invoke-SofCatAsSystem {
    param([string]$Manifest)
    $config = Write-Config $Manifest
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    # The previous run's cmd.exe may not have exited yet, and a start while
    # the task is running is ignored.
    while ((Get-ScheduledTask -TaskName $taskName).State -eq "Running" -and (Get-Date) -lt $deadline) {
        Start-Sleep -Seconds 1
    }
    Remove-Item -LiteralPath $runOut, $runCode -Force -ErrorAction SilentlyContinue
    @(
        "@echo off",
        "`"$sofcatCopy`" -config `"$config`" -verbose > `"$runOut`" 2>&1",
        "echo %ERRORLEVEL% > `"$runCode`""
    ) | Set-Content -LiteralPath $runCmd -Encoding ASCII
    Start-ScheduledTask -TaskName $taskName | Out-Null
    $code = ""
    while ((Get-Date) -lt $deadline) {
        if (Test-Path -LiteralPath $runCode) {
            $code = "$(Get-Content -LiteralPath $runCode -Raw -ErrorAction SilentlyContinue)".Trim()
            if ($code) { break }
        }
        Start-Sleep -Seconds 1
    }
    if (-not $code) { Fail "sofcat ($Manifest) wrote no exit code within $TimeoutSec s" }
    $lines = @()
    if (Test-Path -LiteralPath $runOut) { $lines = @(Get-Content -LiteralPath $runOut) }
    Log ("    sofcat -config {0} as SYSTEM (exit {1})" -f $config, $code)
    $lines | ForEach-Object { Log "      $_" }
    return @{ Code = [int]$code; Out = $lines }
}

# Show-PlainFileRun runs the bad install script with plain -File, outside
# sofcat, and logs whether it would also have failed. Informational only.
function Show-PlainFileRun {
    $dir = Join-Path $WorkDir "plain-file"
    Remove-Item -LiteralPath $dir -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
    $nupkg = Join-Path $dir "$badId.nupkg"
    Invoke-WebRequest -UseBasicParsing -Uri ($BaseUrl.TrimEnd('/') + "/packages/nupkg/$badId-1.0.0.nupkg") -OutFile $nupkg
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [System.IO.Compression.ZipFile]::OpenRead($nupkg)
    try {
        $entry = $zip.GetEntry("tools/install.ps1")
        $script = Join-Path $dir "install.ps1"
        [System.IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $script, $true)
    } finally {
        $zip.Dispose()
    }
    $savedEAP = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $null = & powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $script 2>&1
    } finally {
        $ErrorActionPreference = $savedEAP
    }
    $exit = $LASTEXITCODE
    $continued = Test-Exists $badContinued
    Log ("    INFO plain -File: exit {0}, ran past the unknown command: {1}" -f $exit, $continued)
    Remove-Item -LiteralPath $badContinued -Force -ErrorAction SilentlyContinue
}

$failure = $null
$failed = $false
try {
    New-Item -ItemType Directory -Path (Split-Path $LogPath) -Force | Out-Null
    Set-Content -LiteralPath $LogPath -Value "" -ErrorAction SilentlyContinue

    Write-Step "Preflight: a clean machine without Chocolatey"
    if (-not (Test-Path -LiteralPath $SofCat)) { Fail "sofcat.exe not found at $SofCat" }
    Assert-NoChocolatey
    foreach ($path in @($smokeKept, $badKept, $smokeDir, $badContinued)) { Assert-Missing $path }
    Assert-MarkerMissing $smokeKey
    Assert-MarkerMissing $badKey
    try {
        Invoke-WebRequest -UseBasicParsing -Uri ($BaseUrl.TrimEnd('/') + "/catalogs/nupkg.yaml") | Out-Null
    } catch {
        Fail "cannot fetch $BaseUrl/catalogs/nupkg.yaml: $($_.Exception.Message)"
    }
    Pass "repository reachable at $BaseUrl"

    # SYSTEM runs run.cmd and sofcat.exe from the work directory, so only
    # SYSTEM and Administrators may write there.
    Remove-Item -LiteralPath $WorkDir -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null
    & icacls.exe $WorkDir /inheritance:r /grant:r "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F" | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail "icacls on $WorkDir exited $LASTEXITCODE" }
    Copy-Item -LiteralPath $SofCat -Destination $sofcatCopy -Force

    $action = New-ScheduledTaskAction -Execute "cmd.exe" -Argument "/c `"$runCmd`""
    $principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
    Register-ScheduledTask -TaskName $taskName -Action $action -Principal $principal -Settings $settings -Force | Out-Null
    Pass "registered scheduled task $taskName (SYSTEM)"

    Write-Step "Install: sofcat-nupkg-smoke 1.0.0"
    $r = Invoke-SofCatAsSystem "nupkg_install"
    if ($r.Code -ne 0) { Fail "sofcat (nupkg_install) exited $($r.Code)" }
    Assert-SmokeInstalled "1.0.0"
    Assert-NoChocolatey

    Write-Step "Update: sofcat-nupkg-smoke 2.0.0"
    $r = Invoke-SofCatAsSystem "nupkg_update"
    if ($r.Code -ne 0) { Fail "sofcat (nupkg_update) exited $($r.Code)" }
    Assert-SmokeInstalled "2.0.0"
    Assert-NoChocolatey

    Write-Step "Uninstall: sofcat-nupkg-smoke"
    $r = Invoke-SofCatAsSystem "nupkg_uninstall"
    if ($r.Code -ne 0) { Fail "sofcat (nupkg_uninstall) exited $($r.Code)" }
    Assert-Missing $smokeDir
    Assert-MarkerMissing $smokeKey
    Assert-Missing $smokeKept
    Assert-NoStaging $smokeId
    Assert-NoChocolatey

    Write-Step "Bad package: an unknown command fails the install"
    $r = Invoke-SofCatAsSystem "nupkg_bad"
    $out = $r.Out -join "`n"
    # The script path proves the install got as far as running the script,
    # rather than failing earlier (download, hash, extraction).
    if ($out -notmatch [regex]::Escape(".staging-$badId")) { Fail "sofcat never ran the $badId install script" }
    Pass "sofcat ran the $badId install script"
    if ($out -notmatch "item action failed" -or $out -notmatch "SofCat Nupkg Bad") { Fail "sofcat did not report the $badId install as failed" }
    Pass "sofcat reported the $badId install as failed (exit $($r.Code))"
    if ($out -notmatch "Invoke-SofCatNupkgSmokeNoSuchCommand" -or $out -notmatch "CommandNotFoundException") { Fail "the $badId failure did not come from the unknown command" }
    Pass "the $badId failure came from the unknown command"
    Assert-Missing $badContinued
    Assert-Missing $badKept
    Assert-NoStaging $badId
    Assert-MarkerMissing $badKey
    Assert-NoChocolatey

    Write-Step "Informational: the bad install script under plain -File"
    try {
        Show-PlainFileRun
    } catch {
        Log "    INFO plain -File run did not complete: $($_.Exception.Message)"
    }
} catch {
    $failed = $true
    $failure = $_.Exception.Message
} finally {
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $badContinued -Force -ErrorAction SilentlyContinue
}

if ($failed) {
    Log $failure
    exit 1
}
Log "NUPKG SMOKE PASSED"
exit 0
