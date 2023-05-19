
@echo off

echo **************************************
echo *****  QUICLY-GO BINDINGS REGEN  *****
echo **************************************
echo.

set BASEDIR=%~dp0
echo %BASEDIR%

ECHO [Prerequisites check: C_FOR_GO]
c-for-go -h
if %ERRORLEVEL% EQ 0 goto gen

echo [Build C-FOR-GO]
cd deps/c-for-go
go build -v -o %BASEDIR%/c_for_go.exe
if %ERRORLEVEL% NEQ 0 goto fail

:gen
cd %BASEDIR%
echo [Regen Errors package]
c_for_go.exe -nostamp -nocgo -debug -out quiclylib genspec\errors.yml
if %ERRORLEVEL% NEQ 0 goto fail

echo [Regen Quicly Bindings package]
c_for_go.exe -nostamp -debug -out internal genspec\bindings.yml
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
