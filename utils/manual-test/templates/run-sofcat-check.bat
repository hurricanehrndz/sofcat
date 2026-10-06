@echo off
setlocal

set "SOFCAT_EXE=%ProgramFiles%\SofCat\sofcat.exe"
set "SOFCAT_CONFIG=%ProgramData%\sofcat\config.yaml"
set "EXITCODE=0"

if not exist "%SOFCAT_EXE%" (
  echo Missing executable: %SOFCAT_EXE%
  set "EXITCODE=1"
  goto end
)

if not exist "%SOFCAT_CONFIG%" (
  echo Missing config: %SOFCAT_CONFIG%
  set "EXITCODE=1"
  goto end
)

"%SOFCAT_EXE%" -c "%SOFCAT_CONFIG%" -C -v %*
set "EXITCODE=%errorlevel%"

:end
echo.
pause
exit /b %EXITCODE%
