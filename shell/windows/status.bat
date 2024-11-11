@echo off
tasklist | findstr "git-syncer"
if %ERRORLEVEL% EQU 0 (
    echo Git-Syncer is running
) else (
    echo Git-Syncer is not running
) 