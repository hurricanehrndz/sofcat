# Marker uninstaller for demoblocked (Workstream C self-serve smoke)
$ErrorActionPreference = "Stop"
Remove-Item -Path "C:\ProgramData\sofcat-c-smoke\blocked.txt" -Force -ErrorAction SilentlyContinue
