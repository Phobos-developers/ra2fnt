@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "SCRIPT_DIR=%~dp0"
for %%I in ("%SCRIPT_DIR%..") do set "REPO_ROOT=%%~fI"

set "VERSION=%~1"
if "%VERSION%"=="" (
  for /f %%I in ('git -C "%REPO_ROOT%" rev-parse --short HEAD 2^>nul') do set "GIT_SHA=%%I"
  if defined GIT_SHA (
    set "VERSION=dev-!GIT_SHA!"
  ) else (
    set "VERSION=dev"
  )
)

set "OUT_DIR=%REPO_ROOT%\dist"
if not exist "%OUT_DIR%" mkdir "%OUT_DIR%"

pushd "%REPO_ROOT%"
call :build linux amd64
if errorlevel 1 goto :fail
call :build linux arm64
if errorlevel 1 goto :fail
call :build darwin amd64
if errorlevel 1 goto :fail
call :build darwin arm64
if errorlevel 1 goto :fail
call :build windows amd64
if errorlevel 1 goto :fail
call :build windows arm64
if errorlevel 1 goto :fail
popd

echo done: version=%VERSION%
echo artifacts: %OUT_DIR%
exit /b 0

:build
set "GOOS=%~1"
set "GOARCH=%~2"
set "EXT="
if "%GOOS%"=="windows" set "EXT=.exe"
set "OUT_FILE=%OUT_DIR%\ra2fnt-%GOOS%-%GOARCH%%EXT%"

echo building %OUT_FILE%
set "CGO_ENABLED=0"
set "GOOS=%GOOS%"
set "GOARCH=%GOARCH%"
go build -trimpath -ldflags "-s -w -X main.version=%VERSION%" -o "%OUT_FILE%" ./src/cmd/ra2fnt
if errorlevel 1 exit /b 1
exit /b 0

:fail
popd
echo build failed
exit /b 1
