package quiclylib

import (
	"github.com/Project-Faster/quicly-go/internal/bindings"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
)

var _ types.Session = &QServerSession{}
var _ types.Session = &QClientSession{}
var _ types.Stream = &QStream{}

type Callbacks struct {
	OnStreamOpenCallback  func(stream types.Stream)
	OnStreamCloseCallback func(stream types.Stream, error int)
}

func QuiclyInitializeEngine() int {
	bindings.ResetRegistry()

	result := bindings.QuiclyInitializeEngine()
	return int(result)
}

func QuiclyCloseEngine() int {
	result := bindings.QuiclyCloseEngine()
	return int(result)
}
