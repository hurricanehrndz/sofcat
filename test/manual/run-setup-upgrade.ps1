<#
.SYNOPSIS
setup.exe upgrade-in-place gate: three installer builds, old, new and newer.

.DESCRIPTION
Runs the installers unattended (/S) through these scenarios, in this order,
each as named checks:

  e  foreign   a service named <slug> that runs another binary: the new
               setup refuses, copies nothing and leaves that service alone
  -  old       a fresh install of the old setup
  d  rollback  the new setup over the old one while <slug>-useragent.exe, the
               last file it copies, is held open without write sharing: the
               copy fails, and rollback leaves the old version installed,
               its service running
  a  upgrade   the new setup over the old one: the new version runs, and
               config.yaml and a sentinel tree under Data are untouched
  b  repair    the new setup again over itself
  -  newer     with -NewerSetup only, the newer setup over the new one
  c  downgrade refused, nothing changed: with -NewerSetup the new setup over
               the newer install (c-version-rule, the version rule), then
               the old setup over the installed version (c-old)
  f  uninstall uninstall.exe /S after the upgrades removes everything but
               %ProgramData%\<ProductName>

The intended form takes three installers: -OldSetup the latest released
setup.exe (a real install made by its embala and its own -serviceremove),
-NewSetup and -NewerSetup built from the tree with
`just version=v<new> setup` and `just version=v<newer> setup`. Without
-NewerSetup the version rule goes untested when the old release refuses any
existing service.

Run once on a clean VM (no <slug> service, no install) from an elevated
session; a run leaves the VM uninstalled, with the seeded config.yaml and
sentinel tree under %ProgramData%\<ProductName>. The versions come from the
Add or remove programs entry; when a setup's file name is the
<slug>-<version>-x86_64-setup.exe `just setup` writes, that entry must carry
its <version>.

Exits 0 and prints SETUP UPGRADE PASSED when every check passes; otherwise
lists the failed checks and exits 1. Every line also goes to -LogPath.
PowerShell 5.1 compatible.

.EXAMPLE
.\run-setup-upgrade.ps1 -OldSetup C:\rig\sofcat-5.7.0-x86_64-setup.exe -NewSetup C:\rig\sofcat-5.8.0-x86_64-setup.exe -NewerSetup C:\rig\sofcat-5.9.0-x86_64-setup.exe
#>

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$OldSetup,
    [Parameter(Mandatory = $true)][string]$NewSetup,
    [string]$NewerSetup  = "",
    [string]$ProductName = "SofCat",
    [string]$Slug        = "sofcat",
    [string]$Identifier  = "ca.hrndz.sofcat",
    [string]$LogPath     = "C:\sofcat-test\setup-upgrade.log",
    [int]$TimeoutSec     = 300
)

$ErrorActionPreference = "Stop"

$programFiles = $env:ProgramW6432
if (-not $programFiles) { $programFiles = $env:ProgramFiles }
$installDir = Join-Path $programFiles $ProductName
$exes       = @("$Slug.exe", "$Slug-ui.exe", "$Slug-useragent.exe")
$serviceExe = Join-Path $installDir "$Slug.exe"
$agentExe   = Join-Path $installDir "$Slug-useragent.exe"
$uninstaller = Join-Path $installDir "uninstall.exe"
$dataRoot   = Join-Path $env:ProgramData $ProductName
$config     = Join-Path $dataRoot "config.yaml"
$sentinel   = Join-Path $dataRoot "Data\setup-upgrade-sentinel"
$shortcut   = Join-Path $env:ProgramData "Microsoft\Windows\Start Menu\Programs\$ProductName.lnk"
$runKey     = "Software\Microsoft\Windows\CurrentVersion\Run"
$runValue   = "$Slug-user-sync"
$arpKey     = "Software\Microsoft\Windows\CurrentVersion\Uninstall\$Identifier"
$serviceKey = "SYSTEM\CurrentControlSet\Services\$Slug"
$foreignBin = Join-Path $env:SystemRoot "System32\svchost.exe"

$hklm = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::LocalMachine, [Microsoft.Win32.RegistryView]::Registry64)
$failed = New-Object System.Collections.Generic.List[string]
$held = $null
$foreignCreated = $false

function Log {
    param([string]$Message)
    Write-Host $Message
    try { Add-Content -LiteralPath $LogPath -Value ("{0:o} {1}" -f (Get-Date), $Message) } catch { }
}

# Check records one named check; a failure is listed at the end, and the run
# goes on to the next one.
function Check {
    param([string]$Name, [bool]$Ok, [string]$Detail = "")
    if ($Ok) {
        Log "    ok    $Name $Detail"
    }
    else {
        Log "    FAIL  $Name $Detail"
        $script:failed.Add("$Name $Detail")
    }
}

function Get-RegValue {
    param([string]$Key, [string]$Name)
    $k = $hklm.OpenSubKey($Key)
    if ($null -eq $k) { return $null }
    try { return $k.GetValue($Name, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) } finally { $k.Close() }
}

function Test-RegKey {
    param([string]$Key)
    $k = $hklm.OpenSubKey($Key)
    if ($null -eq $k) { return $false }
    $k.Close()
    return $true
}

# Invoke-Setup runs one installer or uninstaller with /S and returns its exit
# code. The stub is a GUI executable: wait on that process only, since
# Start-Process -Wait would also wait for every process it leaves running.
function Invoke-Setup {
    param([string]$Path)
    Log "    run   $Path /S"
    $p = Start-Process -FilePath $Path -ArgumentList "/S" -PassThru
    $null = $p.Handle
    if (-not $p.WaitForExit($TimeoutSec * 1000)) {
        try { $p.Kill() } catch { }
        Log "    $Path did not exit within $TimeoutSec s"
        return -1
    }
    Log "    exit  $($p.ExitCode)"
    return $p.ExitCode
}

function Get-ServiceStatus {
    $s = Get-Service -Name $Slug -ErrorAction SilentlyContinue
    if ($null -eq $s) { return "" }
    return [string]$s.Status
}

function Wait-ServiceRunning {
    $deadline = (Get-Date).AddSeconds(30)
    while ((Get-ServiceStatus) -ne "Running" -and (Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 500
    }
    return (Get-ServiceStatus)
}

function Get-Hashes {
    $h = @{}
    foreach ($e in $exes) {
        $path = Join-Path $installDir $e
        if (Test-Path -LiteralPath $path -PathType Leaf) {
            $h[$e] = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash
        }
        else {
            $h[$e] = ""
        }
    }
    return $h
}

function Test-SameHashes {
    param([hashtable]$A, [hashtable]$B)
    foreach ($e in $exes) {
        if (-not $A[$e] -or $A[$e] -ne $B[$e]) { return $false }
    }
    return $true
}

# The version a setup file name carries, or "" for another name.
function Get-NamedVersion {
    param([string]$Path)
    $m = [regex]::Match((Split-Path $Path -Leaf), "^$([regex]::Escape($Slug))-(.+)-x86_64-setup\.exe$")
    if ($m.Success) { return $m.Groups[1].Value }
    return ""
}

# Get-ServiceImage returns the full path of the running service's process,
# or "" when it is not running.
function Get-ServiceImage {
    $svc = Get-CimInstance Win32_Service -Filter "Name='$Slug'" -ErrorAction SilentlyContinue
    if ($null -eq $svc -or -not $svc.ProcessId) { return "" }
    $proc = Get-Process -Id $svc.ProcessId -ErrorAction SilentlyContinue
    if ($null -eq $proc) { return "" }
    return [string]$proc.Path
}

function Get-TreeHashes {
    param([string]$Root)
    $h = @{}
    foreach ($f in @(Get-ChildItem -LiteralPath $Root -Recurse -File -Force -ErrorAction SilentlyContinue)) {
        $h[$f.FullName.Substring($Root.Length)] = (Get-FileHash -LiteralPath $f.FullName -Algorithm SHA256).Hash
    }
    return $h
}

function Test-SameTree {
    param([hashtable]$A, [hashtable]$B)
    if ($A.Count -ne $B.Count) { return $false }
    foreach ($k in $A.Keys) {
        if ($A[$k] -ne $B[$k]) { return $false }
    }
    return $true
}

# Assert-Installed checks a working install: the service runs the installed
# executable, the Run value points at the installed helper, the shortcut and
# the ARP entry exist, and the ARP version is $Version.
function Assert-Installed {
    param([string]$Prefix, [string]$Version)
    $status = Wait-ServiceRunning
    Check "$Prefix.service-running" ($status -eq "Running") "(status '$status')"
    $image = Get-ServiceImage
    Check "$Prefix.service-binary" ($image -ieq $serviceExe) "(running '$image')"
    $imagePath = [string](Get-RegValue $serviceKey "ImagePath")
    Check "$Prefix.service-imagepath" ($imagePath -like "`"$serviceExe`"*") "($imagePath)"
    $run = [string](Get-RegValue $runKey $runValue)
    Check "$Prefix.run-value" ($run -ieq "`"$agentExe`"") "($runValue = $run)"
    Check "$Prefix.shortcut" (Test-Path -LiteralPath $shortcut -PathType Leaf) "($shortcut)"
    $arp = [string](Get-RegValue $arpKey "DisplayVersion")
    Check "$Prefix.arp-version" ($arp -eq $Version) "(DisplayVersion '$arp', want '$Version')"
    try { $out = & $serviceExe -version | Out-String } catch { $out = $_.Exception.Message }
    Log "    info  $Slug.exe -version: $($out.Trim())"
}

if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Run this script from an elevated PowerShell session (Run as Administrator)."
}
New-Item -ItemType Directory -Path (Split-Path $LogPath) -Force | Out-Null
$OldSetup = (Resolve-Path $OldSetup).Path
$NewSetup = (Resolve-Path $NewSetup).Path
if ($NewerSetup) { $NewerSetup = (Resolve-Path $NewerSetup).Path }
Log "setup upgrade gate: old $OldSetup, new $NewSetup, newer '$NewerSetup'"

try {
    if ((Get-ServiceStatus) -ne "" -or (Test-Path -LiteralPath $installDir) -or (Test-RegKey $arpKey)) {
        throw "not a clean VM: the $Slug service, $installDir or the ARP entry $arpKey already exists"
    }

    Log "[e] foreign service named $Slug"
    & sc.exe create $Slug binPath= $foreignBin start= demand | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "sc.exe create $Slug exited $LASTEXITCODE" }
    $foreignCreated = $true
    $foreignBefore = [string](Get-RegValue $serviceKey "ImagePath")
    $code = Invoke-Setup $NewSetup
    Check "e.exit-nonzero" ($code -ne 0) "(exit $code)"
    Check "e.nothing-copied" (-not (Test-Path -LiteralPath $installDir)) "($installDir)"
    Check "e.no-arp" (-not (Test-RegKey $arpKey)) "($arpKey)"
    $foreignAfter = [string](Get-RegValue $serviceKey "ImagePath")
    Check "e.foreign-untouched" ($foreignAfter -eq $foreignBefore -and (Get-ServiceStatus) -eq "Stopped") "(ImagePath '$foreignAfter', status '$(Get-ServiceStatus)')"
    & sc.exe delete $Slug | Out-Null
    $foreignCreated = $false

    Log "[old] fresh install of the old setup"
    $code = Invoke-Setup $OldSetup
    Check "old.exit-zero" ($code -eq 0) "(exit $code)"
    $oldVersion = [string](Get-RegValue $arpKey "DisplayVersion")
    $named = Get-NamedVersion $OldSetup
    if ($named) { Check "old.arp-version-named" ($oldVersion -eq $named) "('$oldVersion', file name '$named')" }
    Assert-Installed "old" $oldVersion
    $oldHashes = Get-Hashes

    Log "[d] failed upgrade: $agentExe held open with read sharing only"
    $held = [System.IO.File]::Open($agentExe, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::Read)
    $code = Invoke-Setup $NewSetup
    $held.Close()
    $held = $null
    Check "d.exit-nonzero" ($code -ne 0) "(exit $code)"
    Assert-Installed "d" $oldVersion
    Check "d.executables-old" (Test-SameHashes $oldHashes (Get-Hashes))

    Log "[a] upgrade old -> new"
    New-Item -ItemType Directory -Path (Join-Path $sentinel "nested") -Force | Out-Null
    Set-Content -LiteralPath $config -Encoding ASCII -Value "# seeded by run-setup-upgrade.ps1`r`nurl: http://127.0.0.1:9/`r`nmanifest: setup_upgrade`r`n"
    Set-Content -LiteralPath (Join-Path $sentinel "a.txt") -Encoding ASCII -Value "sentinel a"
    [System.IO.File]::WriteAllBytes((Join-Path $sentinel "nested\b.bin"), [byte[]](0..255))
    $configHash = (Get-FileHash -LiteralPath $config -Algorithm SHA256).Hash
    $sentinelHashes = Get-TreeHashes $sentinel
    $code = Invoke-Setup $NewSetup
    Check "a.exit-zero" ($code -eq 0) "(exit $code)"
    $newVersion = [string](Get-RegValue $arpKey "DisplayVersion")
    $named = Get-NamedVersion $NewSetup
    if ($named) { Check "a.arp-version-named" ($newVersion -eq $named) "('$newVersion', file name '$named')" }
    Check "a.arp-version-changed" ($newVersion -ne $oldVersion) "('$oldVersion' -> '$newVersion')"
    Assert-Installed "a" $newVersion
    $newHashes = Get-Hashes
    Check "a.service-exe-new" ($newHashes["$Slug.exe"] -ne $oldHashes["$Slug.exe"]) "($Slug.exe hash changed)"
    Check "a.config-untouched" ((Test-Path -LiteralPath $config) -and (Get-FileHash -LiteralPath $config -Algorithm SHA256).Hash -eq $configHash) "($config)"
    Check "a.data-untouched" (Test-SameTree $sentinelHashes (Get-TreeHashes $sentinel)) "($sentinel)"

    Log "[b] new over new"
    $code = Invoke-Setup $NewSetup
    Check "b.exit-zero" ($code -eq 0) "(exit $code)"
    Assert-Installed "b" $newVersion
    Check "b.executables-new" (Test-SameHashes $newHashes (Get-Hashes))

    if ($NewerSetup) {
        Log "[newer] upgrade new -> newer"
        $code = Invoke-Setup $NewerSetup
        Check "newer.exit-zero" ($code -eq 0) "(exit $code)"
        $newVersion = [string](Get-RegValue $arpKey "DisplayVersion")
        $named = Get-NamedVersion $NewerSetup
        if ($named) { Check "newer.arp-version-named" ($newVersion -eq $named) "('$newVersion', file name '$named')" }
        Assert-Installed "newer" $newVersion
        $newHashes = Get-Hashes
    }

    if ($NewerSetup) {
        # Both built from the current tree, so only the version rule can refuse.
        Log "[c-version-rule] downgrade refused: new over newer"
        $code = Invoke-Setup $NewSetup
        Check "c-version-rule.exit-nonzero" ($code -ne 0) "(exit $code)"
        Assert-Installed "c-version-rule" $newVersion
        Check "c-version-rule.executables-unchanged" (Test-SameHashes $newHashes (Get-Hashes))
    }

    # A released old setup may refuse any existing service, whatever its
    # version; one built from the current tree refuses by the version rule.
    Log "[c-old] downgrade refused: old over the installed version"
    $code = Invoke-Setup $OldSetup
    Check "c-old.exit-nonzero" ($code -ne 0) "(exit $code)"
    Assert-Installed "c-old" $newVersion
    Check "c-old.executables-unchanged" (Test-SameHashes $newHashes (Get-Hashes))

    Log "[f] uninstall after the upgrades"
    $code = Invoke-Setup $uninstaller
    Check "f.exit-zero" ($code -eq 0) "(exit $code)"
    # The uninstaller deletes itself and the directory from a helper it
    # leaves running after it exits.
    $deadline = (Get-Date).AddSeconds(15)
    while ((Test-Path -LiteralPath $installDir) -and (Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 500
    }
    Check "f.no-service" ((Get-ServiceStatus) -eq "") "(status '$(Get-ServiceStatus)')"
    Check "f.no-run-value" ($null -eq (Get-RegValue $runKey $runValue)) "($runValue)"
    Check "f.no-shortcut" (-not (Test-Path -LiteralPath $shortcut)) "($shortcut)"
    Check "f.no-arp" (-not (Test-RegKey $arpKey)) "($arpKey)"
    $left = ""
    if (Test-Path -LiteralPath $installDir) {
        $left = (@(Get-ChildItem -LiteralPath $installDir -Recurse -Force | ForEach-Object { $_.Name }) -join ", ")
    }
    Check "f.no-install-dir" (-not (Test-Path -LiteralPath $installDir)) "($installDir $left)"
    Check "f.programdata-kept" ((Test-Path -LiteralPath $config) -and (Get-FileHash -LiteralPath $config -Algorithm SHA256).Hash -eq $configHash) "($config)"
    Check "f.data-kept" (Test-SameTree $sentinelHashes (Get-TreeHashes $sentinel)) "($sentinel)"
}
catch {
    Check "run" $false "($($_.Exception.Message))"
}
finally {
    if ($null -ne $held) { $held.Close() }
    if ($foreignCreated) { & sc.exe delete $Slug | Out-Null }
}

if ($failed.Count -gt 0) {
    Log "SETUP UPGRADE FAILED: $($failed.Count) check(s)"
    foreach ($f in $failed) { Log "  $f" }
    exit 1
}
Log "SETUP UPGRADE PASSED"
exit 0
