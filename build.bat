@echo off

:: Get version information
for /f "tokens=*" %%i in ('git describe --tags --always --dirty') do set VERSION=%%i
for /f "tokens=*" %%i in ('git rev-parse --short HEAD') do set COMMIT=%%i
for /f "tokens=*" %%i in ('wmic os get LocalDateTime /value ^| find "="') do set DATETIME=%%i
set BUILD_TIME=%DATETIME:~13,4%-%DATETIME:~17,2%-%DATETIME:~19,2%_%DATETIME:~21,2%:%DATETIME:~23,2%:%DATETIME:~25,2%

:: Build flags
set LDFLAGS=-X main.Version=%VERSION% -X main.GitCommit=%COMMIT% -X main.BuildTime=%BUILD_TIME%

:: Clean old build files
del /f git-syncer.exe 2>nul

:: Execute build
echo Building git-syncer...
go build -ldflags "%LDFLAGS%" -o git-syncer.exe

if %ERRORLEVEL% equ 0 (
    echo Build successful!
    echo Version: %VERSION%
    echo Commit: %COMMIT%
    echo Build time: %BUILD_TIME%
) else (
    echo Build failed!
    exit /b 1
)

 pause