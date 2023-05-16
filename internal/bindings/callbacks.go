package bindings

/*
#include "quicly_wrapper.h"
*/
import "C"

import "fmt"

//export goquicly_on_stream_open
func goquicly_on_stream_open(conn_id C.uint64_t, stream_id C.uint64_t) {
	fmt.Printf("open stream: %d %d\n", uint64(conn_id), uint64(stream_id))
}
