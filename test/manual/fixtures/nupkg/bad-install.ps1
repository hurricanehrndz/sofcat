# The install script of sofcat-nupkg-bad (test/manual/run-nupkg-smoke.ps1). It
# calls a command that does not exist. sofcat runs package scripts with
# $ErrorActionPreference = 'Stop', so the install fails at that call and the
# marker below is never written; under plain -File it would be.
Invoke-SofCatNupkgSmokeNoSuchCommand
Set-Content -LiteralPath (Join-Path $env:ProgramData 'sofcat-nupkg-bad-continued.txt') -Value 'continued'
