
@echo off

echo **************************************
echo *****  QUICLY-GO BINDINGS REGEN  *****
echo **************************************
echo.

set BASEDIR=%~dp0

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
c-for-go.exe -nostamp -nocgo -debug -path %BASEDIR% -out quiclylib genspec\errors.win32.yml
if %ERRORLEVEL% NEQ 0 goto fail

echo [Regen Quicly Bindings package]
c-for-go.exe -nostamp -debug -withsync -path %BASEDIR% -out internal genspec\bindings.win32.yml
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
