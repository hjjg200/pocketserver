// +build linux,386
// linux and 386
// build for iSH


package main

import (
	"fmt"
	"strings"
	"time"
	"os/signal"
	"runtime/trace"
	"syscall"
	"os"
	"sync"
)

func runTests() {

	fs := []struct{
		r rune
		n string
		f func()
	}{
		{ 'a', "Channel tests", _testChan },
		{ 'f', "FFmpeg test", _testFFmpeg },
		{ 'q', "Pipe block test", _testPipeBlock },
	}

	// Trace file (check non existent)
	tpath := fmt.Sprintf("test%d-trace.txt", time.Now().Unix())
	tf, err := ioOpenFile(tpath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	must(err)
	must(tf.Close())

	// Listen for signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("USER EXITED")
		must(tf.Close())
		must(ioRemove(tpath))
		os.Exit(0)
	}()

	fmt.Println()
	fmt.Println("TEST")
	fmt.Println("Test option is", gAppInfo.Test)
	i := 0
	for {
		i++
		found := 0
		for j, a := range fs {
			if strings.ContainsRune(gAppInfo.Test, a.r) {
				fmt.Println()
				fmt.Printf("TEST LOOP %d - %d: %s\n", i, j, a.n)
				func() {
					// Truncate the trace file
					tf, err = ioOpenFile(tpath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
					must(err)
					must(trace.Start(tf))
					defer func() {
						if r := recover(); r != nil {
							logFatal("Recovered from", r)
						}
					}()
					a.f()
					trace.Stop()
					must(tf.Close())
				}()
				found++
			}
		}

		if found == 0 {
			logFatal("No tests found")
		}
		time.Sleep(500 * time.Millisecond)
	}

}

func _testChan() {
	n := 100
	fmt.Printf("%d channel sends and receives\n", n)

	for i := 0; i < n; i++ {
		ch := make(chan struct{})
		go func() {
			ch <-struct{}{}
		}()
		<-ch
		fmt.Printf("\r%d", i)
	}
	fmt.Printf("\rDone\n")

}

func _testFFmpeg() {

	gAppInfo.Debug2 += "fi"

	// Other args
	if gAppInfo.TestVar == "1" {

		// TEST RESULT MEMO
		/*
		Even doing just ls in succession causes panic in go and that panic is raised from
		go internal page allocation functions; exceptions include
		- index out of bounds
		- division by zero
		*/
		args := []string{"ls"}
		wait, _, err := _executeFFmpeg(args, ioStdout, ioStderr)
		if err != nil {
			logFatal(err)
		}
		<-wait

	} else {

		args := []string{"ffmpeg", "-version"}
		err := subFFmpeg(args)
		if err != nil {
			logDebug2('f', 10, err)
			err = executeFFmpeg(args, nil, nil)
			if err != nil {
				logFatal(err)
			}
		}

	}


}


func _testPipeBlock() {
	gAppInfo.Debug2 += "ip"

	n := 100
	msg := "OK"
	var mu sync.Mutex

	fmt.Printf("%d wait blocked reads\n", n)
	for i := 0; i < n; i++ {
		r, w, err := ioPipe()
		if err != nil {
			logFatal(err)
		}
		mu.Lock()
		go func() {
			defer mu.Unlock()
			n, werr := w.Write([]byte(msg))
			fmt.Print("w")
			if werr != nil {
				logFatal("Write error:", werr)
			}
			if n != len(msg) {
				logFatal("Not matching write bytes", n, len(msg))
			}
		}()
		buf := make([]byte, 32)
		n, err := r.Read(buf)
		fmt.Print("r")
		if err != nil {
			logFatal("Read error:", err)
		}
		if n != len(msg) {
			logFatal("Not matching read bytes", n, len(msg))
		}
		must(r.Close())
		must(w.Close())
		fmt.Print("c")
		mu.Lock()
		fmt.Print("  ")
		mu.Unlock()
	}
	fmt.Println()

	fmt.Printf("%d os.Pipe blocked writes\n", n)
	fmt.Println("This test fails with wcr")
	for i := 0; i < n; i++ {
		r, w, err := os.Pipe()
		if err != nil {
			logFatal(err)
		}
		mu.Lock()
		go func() {
			defer mu.Unlock()
			buf := make([]byte, 32)
			n, rerr := r.Read(buf)
			fmt.Print("r")
			if rerr != nil {
				logFatal("Read error:", rerr)
			}
			if n != len(msg) {
				logFatal("Not matching read bytes", n, len(msg))
			}
		}()
		n, err := w.Write([]byte(msg))
		fmt.Print("w")
		if err != nil {
			logFatal("Write error:", err)
		}
		if n != len(msg) {
			logFatal("Not matching write bytes", n, len(msg))
		}
		must(r.Close())
		must(w.Close())
		fmt.Print("c")
		mu.Lock()
		fmt.Print("  ")
		mu.Unlock()
	}
	fmt.Println()

	fmt.Printf("%d wait blocked writes\n", n)
	fmt.Println("This test fails with wcr")
	for i := 0; i < n; i++ {
		r, w, err := ioPipe()
		if err != nil {
			logFatal(err)
		}
		mu.Lock()
		go func() {
			defer mu.Unlock()
			buf := make([]byte, 32)
			n, rerr := r.Read(buf)
			fmt.Print("r")
			if rerr != nil {
				logFatal("Read error:", rerr)
			}
			if n != len(msg) {
				logFatal("Not matching read bytes", n, len(msg))
			}
		}()
		n, err := w.Write([]byte(msg))
		fmt.Print("w")
		if err != nil {
			logFatal("Write error:", err)
		}
		if n != len(msg) {
			logFatal("Not matching write bytes", n, len(msg))
		}
		must(r.Close())
		must(w.Close())
		fmt.Print("c")
		mu.Lock()
		fmt.Print("  ")
		mu.Unlock()
	}
	fmt.Println()
}