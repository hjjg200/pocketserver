// +build !linux !386
// !linux or !386

package main

import (
	"os/exec"
	"fmt"
	"os"
	"runtime"
)

func _executeFFmpeg(args []string, stdout, stderr *ioFile) (<-chan struct{}, func() error, error) {

	command := joinCommandArgs(args)
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-Command", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	logDebug(cmd)

	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Start()
	if err != nil {
		logDebug("FFmpeg error", err)
		return nil, nil, fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	wait := make(chan struct{})
	go func() {
		cmd.Wait()
		wait <-struct{}{}
	}()
	return wait, cmd.Process.Kill, nil

}
