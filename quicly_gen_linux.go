//go:generate bash -x deps_build.sh
//go:generate bash -x regen.sh
//go:generate bash -c "cd tester/ && rm -f ./tester && go build -v -o tester"

package quicly
