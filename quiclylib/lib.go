package quiclylib

import (
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	"os"
	"path/filepath"
)

var _ types.ServerSession = &QServerSession{}
var _ types.ClientSession = &QClientSession{}
var _ types.Stream = &QStream{}

const (
	READ_SIZE         = 512 * 1024
	SMALL_BUFFER_SIZE = 4 * 1024
)

func QuiclyInitializeEngine(alpn, certfile, certkey string, idle_timeout uint64) int {
	bindings.ResetRegistry()

	cwd, _ := os.Getwd()
	if !filepath.IsAbs(certfile) {
		certfile = filepath.Join(cwd, certfile)
	}
	if !filepath.IsAbs(certkey) {
		certkey = filepath.Join(cwd, certkey)
	}

	result := bindings.QuiclyInitializeEngine(alpn, certfile, certkey, idle_timeout)
	return int(result)
}

func QuiclyCloseEngine() int {
	result := bindings.QuiclyCloseEngine()
	return int(result)
}
