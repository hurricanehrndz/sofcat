# Marker installer for demolab (installable_condition smoke)
$ErrorActionPreference = "Stop"
$dir = "C:\ProgramData\sofcat-c-smoke"
New-Item -ItemType Directory -Path $dir -Force | Out-Null
Set-Content -Path (Join-Path $dir "lab.txt") -Value "demolab" -Encoding ASCII
