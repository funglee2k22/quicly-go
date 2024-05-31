//go:generate zsh deps_build.sh
//go:generate zsh regen.sh
//go:generate zsh -c "cd tester/ && rm -f ./tester && go build -v -o tester"

package quicly
