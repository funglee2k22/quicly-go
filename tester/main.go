package main

import (
	log "github.com/rs/zerolog"

	"github.com/Project-Faster/quicly-go"
	"os"
)

func main() {
	var connection quicly.Quicly

	var l = log.New(os.Stdout).With().Timestamp().Logger()
	connection.Initialize(quicly.Options{
		Logger: &l,
		OnOpen: func() {

		},
		OnClose: func() {

		},
	})

	connection.Listen(nil)

	connection.Dial(nil)

	connection.Terminate()
}
