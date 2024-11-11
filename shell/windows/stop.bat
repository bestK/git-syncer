@echo off
for /f "tokens=2" %%a in ('tasklist ^| findstr "git-syncer"') do (
    taskkill /F /PID %%a
)
if exist git-syncer.pid del git-syncer.pid 