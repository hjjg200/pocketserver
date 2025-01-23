// +build linux,386
// linux and 386
// build for iSH

package main

/*
#include <stdlib.h>
#include <string.h>
#include "ffmpeg_ish.h"
*/
import "C"
import (
	"unsafe"
)


func executeFFmpeg(command string) (string, error) {

	command = "nice " + command // nice -n 10~19

	// Allocate a buffer for the output
	output := make([]byte, 8192)
	cOutput := (*C.char)(unsafe.Pointer(&output[0]))

	// Call the C function
	cCommand := C.CString(command)
	defer C.free(unsafe.Pointer(cCommand))

	C.execute_ffmpeg(cCommand, cOutput, C.size_t(len(output)))
	
	// Ignore status code
	/*status :=
	if status != 0 {
		return "", fmt.Errorf("ffmpeg execution failed with status: %d", status)
	}*/

	// Convert C output to Go string
	return C.GoString(cOutput), nil
}


