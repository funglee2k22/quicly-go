package quiclylib

import (
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
)

var _ types.Session = &QServerSession{}
var _ types.Session = &QClientSession{}
var _ types.Stream = &QStream{}

func QuiclyInitializeEngine() int {
	bindings.QuiclyResetRegistry()

	result := bindings.QuiclyInitializeEngine()
	return int(result)
}

func QuiclyCloseEngine() int {
	result := bindings.QuiclyCloseEngine()
	return int(result)
}
