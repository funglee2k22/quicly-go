package quiclylib

import (
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
)

var _ types.Session = &QServerSession{}
var _ types.Session = &QClientSession{}
var _ types.Stream = &QStream{}

const (
	READ_SIZE         = 512 * 1024
	SMALL_BUFFER_SIZE = 4 * 1024
)

func QuiclyInitializeEngine(certfile string, certkey string) int {
	bindings.ResetRegistry()

	result := bindings.QuiclyInitializeEngine(certfile, certkey)
	return int(result)
}

func QuiclyCloseEngine() int {
	result := bindings.QuiclyCloseEngine()
	return int(result)
}
