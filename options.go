package quicly

import log "github.com/rs/zerolog"

type Options struct {
	Logger *log.Logger

	// custom callbacks
	OnOpen  func()
	OnClose func()
}
