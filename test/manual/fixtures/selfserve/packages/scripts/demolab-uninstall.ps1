# Marker uninstaller for demolab (installable_condition smoke)
$ErrorActionPreference = "Stop"
Remove-Item -Path "C:\ProgramData\sofcat-c-smoke\lab.txt" -Force -ErrorAction SilentlyContinue
