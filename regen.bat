
@echo off

echo **************************************
echo *****  QUICLY-GO BINDINGS REGEN  *****
echo **************************************
echo.

set BASEDIR=%~dp0
go env GOOS > tmpfile
set /p GOOS= < tmpfile

go env GOARCH > tmpfile
set /p GOARCH= < tmpfile
del tmpfile

echo [%GOOS%]
echo [%GOARCH%]

ECHO [Prerequisites check: C_FOR_GO]
c-for-go.exe -h
if %ERRORLEVEL% EQU 0 goto gen

echo [Build C-FOR-GO]
cd deps/c-for-go
go install -v
if %ERRORLEVEL% NEQ 0 goto fail

:gen
cd %BASEDIR%
echo [Regen Errors package]
c-for-go.exe -nostamp -nocgo -debug -path %BASEDIR% -out quiclylib genspec\errors.windows.yml
if %ERRORLEVEL% NEQ 0 goto fail

echo [Regen Quicly Bindings package]
c-for-go.exe -nostamp -debug -path %BASEDIR% -out internal\%GOOS%\%GOARCH% genspec\bindings.windows.yml
if %ERRORLEVEL% NEQ 0 goto fail

:ok
echo.
echo *************************************************
echo ****             RESULT: SUCCESS             ****
echo **** Check generate sources for correctness! ****
echo *************************************************
cd %BASEDIR%
exit /B 0

:fail
echo.
echo ********************************
echo **** RESULT: FAILURE        ****
echo ********************************
cd %BASEDIR%
exit /B 1
