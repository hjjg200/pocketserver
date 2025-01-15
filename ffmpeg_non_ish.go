// +build !linux !386
// !linux or !386

package main

import (
	"os/exec"
	"fmt"
	"bytes"
	"runtime"
)

func executeFFmpeg(command string) (string, error) {

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-Command", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	logDebug(cmd)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		logDebug("FFmpeg error", err)
		// FFmpeg returns an error code for certain operations, but metadata is still printed
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() != 0 {
			err = nil
		} else {
			return "", fmt.Errorf("failed to execute ffmpeg: %w", err)
		}
	}

	return stderr.String(), nil

}