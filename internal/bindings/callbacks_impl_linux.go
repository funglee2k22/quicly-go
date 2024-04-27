package bindings

/*
#include "quicly_wrapper.h"
*/
import "C"

import (
	"unsafe"
)

func IovecToBytes(data Iovec) []byte {
	return unsafe.Slice((*byte)(data.Iov_base), int(data.Iov_len))
}
