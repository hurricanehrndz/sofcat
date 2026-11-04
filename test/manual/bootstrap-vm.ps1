<#
.SYNOPSIS
Bootstrap SofCat on a Windows VM from prepared manual-test assets.

.DESCRIPTION
- Downloads sofcat.exe and sofcat-ui.exe from a provided base URL.
- Writes config.yaml to ProgramData.
- Optionally installs/starts service mode.

A branded build passes its brand.env SLUG as -Slug and NAME as -ProductName;
the exe names, service name and default paths follow from them.

-ListHeading writes a branding: block with list_heading, the storefront's
heading in place of "Available software". config.yaml is written as ASCII,
so the heading must be ASCII.
#>

[CmdletBinding()]
param(
    [string]$BaseUrl = "http://localhost:8080/",
    [string]$Slug = "sofcat",
    [string]$ProductName = "SofCat",
    [string]$InstallPath = "$env:ProgramFiles\$ProductName",
    [string]$ConfigPath = "$env:ProgramData\$ProductName\config.yaml",
    [string]$AppDataPath = "$env:ProgramData\$ProductName\Data",
    [string]$Manifest = "example_manifest",
    [string[]]$Catalogs = @("example_catalog"),
    [string]$ListHeading = "",
    [switch]$InstallService,
    [switch]$StartService,
    [switch]$NoPause
)

$ErrorActionPreference = "Stop"

function Write-Step {
    param([string]$Message)
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Convert-ToYamlPath {
    param([string]$PathValue)
    return ($PathValue -replace "\\", "/")
}

function Write-SofCatConfig {
    param(
        [string]$ConfigFilePath,
        [string]$URLValue,
        [string]$ManifestValue,
        [string[]]$CatalogList,
        [string]$AppDataPathValue,
        [string]$ListHeadingValue = ""
    )

    $configDir = Split-Path -Parent $ConfigFilePath
    New-Item -ItemType Directory -Path $configDir -Force | Out-Null
    New-Item -ItemType Directory -Path $AppDataPathValue -Force | Out-Null

    $yamlCatalogs = ($CatalogList | ForEach-Object { "  - $_" }) -join "`r`n"
    $yamlAppDataPath = Convert-ToYamlPath -PathValue $AppDataPathValue

    $content = @"
url: $URLValue
url_packages: $URLValue
manifest: $ManifestValue
catalogs:
$yamlCatalogs
app_data_path: $yamlAppDataPath
# service_name: $Slug
# service_interval: 1h
# service_pipe_name: $Slug-service
"@

    if ($ListHeadingValue) {
        if ($ListHeadingValue -match '[^\x20-\x7E]') {
            throw "-ListHeading must be printable ASCII: config.yaml is written as ASCII"
        }
        # A single-quoted YAML scalar takes any printable text; only ' is doubled.
        $quoted = "'" + ($ListHeadingValue -replace "'", "''") + "'"
        $content += "`r`nbranding:`r`n  list_heading: $quoted"
    }

    Set-Content -Path $ConfigFilePath -Value $content -Encoding ASCII
}

function Ensure-PathEntry {
    param([string]$DirectoryPath)

    $resolvedDir = (Resolve-Path $DirectoryPath).Path.TrimEnd("\")

    $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
    if (-not $machinePath) {
        $machinePath = ""
    }

    $machineEntries = @($machinePath -split ";" | Where-Object { $_ -ne "" })
    $normalizedMachineEntries = @($machineEntries | ForEach-Object { $_.TrimEnd("\").ToLowerInvariant() })
    $normalizedDir = $resolvedDir.ToLowerInvariant()

    if ($normalizedMachineEntries -notcontains $normalizedDir) {
        $newMachinePath = if ($machinePath) { "$machinePath;$resolvedDir" } else { $resolvedDir }
        [Environment]::SetEnvironmentVariable("Path", $newMachinePath, "Machine")
        Write-Step "Added $resolvedDir to machine PATH"
    }
    else {
        Write-Step "Install path already exists in machine PATH"
    }

    $processEntries = @($env:Path -split ";" | Where-Object { $_ -ne "" })
    $normalizedProcessEntries = @($processEntries | ForEach-Object { $_.TrimEnd("\").ToLowerInvariant() })
    if ($normalizedProcessEntries -notcontains $normalizedDir) {
        $env:Path = if ($env:Path) { "$env:Path;$resolvedDir" } else { $resolvedDir }
    }
}

# Get-Service -Name also matches display names, and a second brand on the
# machine may carry the same display name, so match the service name exactly.
function Get-ServiceByName {
    param([string]$ServiceName)
    Get-Service -ErrorAction SilentlyContinue | Where-Object { $_.Name -eq $ServiceName }
}

function Remove-ExistingSofCatService {
    param([string]$ServiceName = $Slug)

    $existingService = Get-ServiceByName $ServiceName
    if (-not $existingService) {
        return
    }

    Write-Step "Existing '$ServiceName' service detected; stopping and removing it"

    # Prevent SCM from auto-restarting the service while we are replacing sofcat.exe.
    sc.exe failure $ServiceName reset= 0 actions= "" | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Warning "Unable to clear failure actions for service '$ServiceName'."
    }
    sc.exe failureflag $ServiceName 0 | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Warning "Unable to clear failure flag for service '$ServiceName'."
    }

    if ($existingService.Status -ne [System.ServiceProcess.ServiceControllerStatus]::Stopped) {
        sc.exe stop $ServiceName | Out-Null
        $serviceStopped = $false
        for ($i = 0; $i -lt 30; $i++) {
            Start-Sleep -Seconds 1
            $serviceState = Get-ServiceByName $ServiceName
            if (-not $serviceState -or $serviceState.Status -eq [System.ServiceProcess.ServiceControllerStatus]::Stopped) {
                $serviceStopped = $true
                break
            }
        }
        if (-not $serviceStopped) {
            $serviceInfo = Get-CimInstance -ClassName Win32_Service -Filter "Name='$ServiceName'" -ErrorAction SilentlyContinue
            if ($serviceInfo -and $serviceInfo.ProcessId -gt 0) {
                Write-Step "Service '$ServiceName' did not stop in time; terminating PID $($serviceInfo.ProcessId)"
                Stop-Process -Id $serviceInfo.ProcessId -Force -ErrorAction Stop

                for ($i = 0; $i -lt 10; $i++) {
                    Start-Sleep -Seconds 1
                    $serviceState = Get-ServiceByName $ServiceName
                    if (-not $serviceState -or $serviceState.Status -eq [System.ServiceProcess.ServiceControllerStatus]::Stopped) {
                        $serviceStopped = $true
                        break
                    }
                }
            }
        }

        if (-not $serviceStopped) {
            throw "Timed out waiting for service '$ServiceName' to stop, even after force-terminating its process."
        }
    }

    sc.exe delete $ServiceName | Out-Null

    for ($i = 0; $i -lt 30; $i++) {
        Start-Sleep -Milliseconds 250
        if (-not (Get-ServiceByName $ServiceName)) {
            Write-Step "Removed existing '$ServiceName' service"
            return
        }
    }

    throw "Timed out waiting for service '$ServiceName' to be removed."
}

if (-not (Test-IsAdministrator)) {
    throw "Run this script from an elevated PowerShell session (Run as Administrator)."
}

if (-not $BaseUrl.EndsWith("/")) {
    $BaseUrl = "$BaseUrl/"
}

New-Item -ItemType Directory -Path $InstallPath -Force | Out-Null
$binaryPath = Join-Path $InstallPath "$Slug.exe"
$binaryUrl = "$BaseUrl" + "$Slug.exe"
$uiBinaryPath = Join-Path $InstallPath "$Slug-ui.exe"
$uiBinaryUrl = "$BaseUrl" + "$Slug-ui.exe"

Remove-ExistingSofCatService

Write-Step "Downloading $ProductName binary from $binaryUrl"
Invoke-WebRequest -Uri $binaryUrl -OutFile $binaryPath
$runningUI = @(Get-Process -Name "$Slug-ui" -ErrorAction SilentlyContinue)
if ($runningUI.Count -gt 0) {
    Write-Step "Stopping running $ProductName UI so $uiBinaryPath can be replaced"
    $runningUI | Stop-Process -Force
    Start-Sleep -Seconds 2
}

Write-Step "Downloading $ProductName UI binary from $uiBinaryUrl"
Invoke-WebRequest -Uri $uiBinaryUrl -OutFile $uiBinaryPath

Write-Step "Writing config to $ConfigPath"
Write-SofCatConfig -ConfigFilePath $ConfigPath -URLValue $BaseUrl -ManifestValue $Manifest -CatalogList $Catalogs -AppDataPathValue $AppDataPath -ListHeadingValue $ListHeading
Ensure-PathEntry -DirectoryPath $InstallPath

if ($InstallService) {
    Write-Step "Installing $ProductName Windows service"
    & $binaryPath -c $ConfigPath -serviceinstall
}

if ($StartService) {
    Write-Step "Starting $ProductName Windows service"
    & $binaryPath -c $ConfigPath -servicestart
}

Write-Step "Bootstrap complete"
Write-Host "Binary: $binaryPath"
Write-Host "UI binary: $uiBinaryPath"
Write-Host "Config: $ConfigPath"
Write-Host ""
Write-Host "Install path added to PATH: $InstallPath"
Write-Host "New CMD/PowerShell windows will pick up PATH automatically."
Write-Host ""
Write-Host "Manual test command:"
Write-Host "& `"$binaryPath`" -c `"$ConfigPath`" -C -v"

if (-not $NoPause) {
    Write-Host ""
    Write-Host "Press any key to continue..."
    [void]$Host.UI.RawUI.ReadKey("NoEcho,IncludeKeyDown")
}
