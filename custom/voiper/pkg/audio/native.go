package audio

// #include <stdlib.h>
import "C"
import "unsafe"

func freeDeviceID(id unsafe.Pointer) { C.free(id) }
