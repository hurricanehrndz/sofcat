@echo off

echo Building SofCat MSI using WIX

for /f %%i in ('git describe --tags --always --dirty') do set versionString=%%i
echo Version: %versionString%

if "%PRODUCT_VERSION%"=="" (
  set "productVersion=%versionString%"
) else (
  set "productVersion=%PRODUCT_VERSION%"
)

if "%productVersion:~0,1%"=="v" set "productVersion=%productVersion:~1%"
for /f "tokens=1 delims=-+" %%i in ("%productVersion%") do set "productVersion=%%i"
echo ProductVersion: %productVersion%

copy "..\build\sofcat.exe" sofcat.exe 1>NUL

echo Running candle...
call "%wix%bin\candle.exe" -dProductVersion=%productVersion% sofcat.wxs 1>NUL

echo Running light...
call "%wix%bin\light.exe" -ext WixUtilExtension.dll sofcat.wixobj 1>NUL

echo Cleaning up...
move sofcat.msi sofcat-%versionString%.msi 1>NUL
del sofcat.exe
del sofcat.wixpdb
del sofcat.wixobj

pause
