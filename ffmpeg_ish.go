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
	"fmt"
)

func _executeFFmpeg(args []string, stdout, stderr *ioFile) (<-chan struct{}, func() error, error) {

	cStdout := C.int(-1)
	cStderr := C.int(-1)

	if (stdout != nil) {
		cStdout = C.int(stdout.Fd())
	}
	if (stderr != nil) {
		cStderr = C.int(stderr.Fd())
	}

	//
	args = append([]string{"nice"}, args...)
    cArgs := make([]*C.char, len(args)+1) // +1 for NULL termination
    for i, arg := range args {
        cArgs[i] = C.CString(arg)
    }
    cArgs[len(args)] = nil // Null terminate

    // Convert Go slice to C array
    cArgPtr := (**C.char)(unsafe.Pointer(&cArgs[0]))
	defer func() {
		for _, cStr := range cArgs {
			C.free(unsafe.Pointer(cStr))
		}
	}()

    // Free allocated C strings

	/*
	// Call the C function
	command := "nice " + joinCommandArgs(args) // nice -n 10~19
	cCommand := C.CString(command)
	defer C.free(unsafe.Pointer(cCommand))

	pid := C.start_ffmpeg(cCommand, cStdout, cStderr)
	if pid < 0 {
		return nil, nil, fmt.Errorf("Failed to start ffmpeg process")
	}
	*/

	pid := C.start_ffmpeg(cArgPtr, cStdout, cStderr)
	if pid < 0 {
		return nil, nil, fmt.Errorf("Failed to start ffmpeg process")
	}

	wait := make(chan struct{})
	go func() {
		C.wait_process(pid)
		wait <-struct{}{}
	}()

	terminator := func() error {
		r := C.terminate_process(pid, 1) // SIGKILL
		if r != 0 {
			return fmt.Errorf("Failed to kill ffmpeg")
		}
		return nil
	}
	
	// TODO process exit code error handling
	return wait, terminator, nil
}



func executeFFmpegPopen(args []string) (string, error) {

	command := "nice " + joinCommandArgs(args) // nice -n 10~19

	// Allocate a buffer for the output
	output := make([]byte, 8192)
	cOutput := (*C.char)(unsafe.Pointer(&output[0]))

	// Call the C function
	cCommand := C.CString(command)
	defer C.free(unsafe.Pointer(cCommand))

	C.execute_ffmpeg_popen(cCommand, cOutput, C.size_t(len(output)))
	
	// Ignore status code
	/*status :=
	if status != 0 {
		return "", fmt.Errorf("ffmpeg execution failed with status: %d", status)
	}*/

	// Convert C output to Go string
	return C.GoString(cOutput), nil
}


