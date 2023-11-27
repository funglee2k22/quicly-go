//go:generate cmd /c "deps_build.bat"
//go:generate cmd /c "regen.bat"
//go:generate cmd /c "cd tester/ && del tester.exe && go build -v -o tester.exe"

package quicly
