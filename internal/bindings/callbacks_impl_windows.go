package bindings

/*
#include "quicly_wrapper.h"
*/
import "C"

func IovecToBytes(data Iovec) []byte {
	tmp := append([]byte{}, data.Iov_base...)
	return tmp
}
