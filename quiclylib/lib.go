package quiclylib

import (
	"github.com/Project-Faster/quicly-go/internal/bindings"
)

func QuiclyInitializeEngine() int {
	result := bindings.QuiclyInitializeEngine()
	return int(result)
}

func QuiclyCloseEngine() int {
	result := bindings.QuiclyCloseEngine()
	return int(result)
}
