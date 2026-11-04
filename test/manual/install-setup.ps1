<#
.SYNOPSIS
Run the setup.exe installer, or the uninstaller it leaves behind, unattended.

.DESCRIPTION
Runs `<setup.exe> /S`, or with -Uninstall `"$env:ProgramFiles\<ProductName>\uninstall.exe" /S`,
and waits for it: the stub is a GUI executable, so a plain call would return
before it has done anything. Prints the exit code and exits with it, so a
caller over SSH can gate on it. A per-machine install needs an elevated
session; from an unelevated one the installer relaunches itself with a UAC
prompt nobody can answer over SSH.

After an uninstall it waits a little for the directory to go, since the
uninstaller deletes itself from a detached helper after it exits.

.EXAMPLE
.\install-setup.ps1 -Setup C:\sofcat-test\sofcat-1.0.0-x86_64-setup.exe
.\install-setup.ps1 -Uninstall
.\install-setup.ps1 -Uninstall -ProductName "Tapas Test"
#>

[CmdletBinding()]
param(
    [string]$Setup = "",
    [string]$ProductName = "SofCat",
    [switch]$Uninstall
)

$ErrorActionPreference = "Stop"

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

if (-not (Test-IsAdministrator)) {
    throw "Run this script from an elevated PowerShell session (Run as Administrator)."
}

$installDir = Join-Path $env:ProgramFiles $ProductName
if ($Uninstall) {
    $exe = Join-Path $installDir "uninstall.exe"
}
else {
    if (-not $Setup) {
        throw "-Setup <path to setup.exe> is required unless -Uninstall is given."
    }
    $exe = (Resolve-Path $Setup).Path
}
if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) {
    throw "$exe not found."
}

Write-Host "==> Running $exe /S" -ForegroundColor Cyan
$process = Start-Process -FilePath $exe -ArgumentList "/S" -Wait -PassThru
$exitCode = $process.ExitCode
Write-Host "exit code: $exitCode"

if ($Uninstall -and $exitCode -eq 0) {
    for ($i = 0; $i -lt 30; $i++) {
        if (-not (Test-Path -LiteralPath $installDir)) {
            break
        }
        Start-Sleep -Milliseconds 500
    }
    if (Test-Path -LiteralPath $installDir) {
        $left = @(Get-ChildItem -LiteralPath $installDir -Force | ForEach-Object { $_.Name })
        Write-Warning "$installDir is still present: $($left -join ', ')"
    }
    else {
        Write-Host "$installDir removed"
    }
}

exit $exitCode
