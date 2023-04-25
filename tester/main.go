package main

import (
	"github.com/Project-Faster/quicly-go"
)

func main() {
	var connection quicly.Quicly

	connection.Initialize(nil)

	connection.Terminate()
}
