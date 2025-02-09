package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"strconv"
	"time"
	"log"
	"io"
	"syscall"
	"strings"
)

const name = "dirtest3"

// ReadDirectory reads a directory using ReadDir with a chunk size and repeats the process repeatCount times.
func ReadDirectory(root string, chunkSize int, sleepDuration time.Duration) {

	log.Println("ReadDir stress test")
	log.Println(root)
	log.Printf("chunkSize: %d, sleepDuration: %v\n", chunkSize, sleepDuration)
	log.Println()

	allStart := time.Now()

	for cycle := 1; cycle <= 100; cycle++ {

		currentIndex := 0
		start := time.Now()
		slept := time.Duration(0)

		var readDir func(path string)
		readDir = func(path string) {
			file, err := os.Open(path)
			if err != nil {
				log.Fatalf("Error opening directory: %v\n", err)
			}
			defer file.Close()

			for {
				entries, err := file.ReadDir(chunkSize)
				if err != nil && err != fs.ErrClosed {
					log.Fatalf("Error reading directory: %v\n", err)
				}

				for _, entry := range entries {
					currentIndex++
					fmt.Printf("\r" + strings.Repeat(".", int( (time.Now().UnixMilli()/500)%3+1 )) + "    \r")

					// Discard the name (as per instructions)
					_ = entry.Name()

					if entry.IsDir() {
						// Recursively read subdirectory
						subDirPath := fmt.Sprintf("%s/%s", path, entry.Name())
						readDir(subDirPath)
					}
				}

				if len(entries) < chunkSize {
					// No more entries left to read
					break
				}

				slept += sleepDuration
				time.Sleep(sleepDuration)
			}
		}

		readDir(root)
		elapsed := time.Since(start)
		fmt.Printf("\r")
		log.Printf("Cycle %d: %d files (%v/%v)\n", cycle, currentIndex, elapsed-slept, elapsed)
		time.Sleep(sleepDuration)
	}

	log.Println("SUCCESS", time.Since(allStart))
}

func main() {

	runtime.GOMAXPROCS(1)
	
	if len(os.Args) < 4 {
		log.Fatalln("Usage: go run main.go <path> <chunkSize> <sleepDurationMs>")
	}

	rootDir := os.Args[1]
	chunkSize, err := strconv.Atoi(os.Args[2])
	if err != nil || chunkSize <= 0 {
		log.Fatalln("Invalid chunk size. Please provide a positive integer.")
	}

	sleepDurationMs, err := strconv.Atoi(os.Args[3])
	if err != nil || sleepDurationMs < 0 {
		log.Fatalln("Invalid sleep duration. Please provide a non-negative integer.")
	}
	sleepDuration := time.Duration(sleepDurationMs) * time.Millisecond


	// Open a log file
	logFile, err := os.OpenFile(name+".log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalln("Failed to open log file: %v", err)
	}
	defer logFile.Close()
	log.SetFlags(0)
	log.SetOutput(io.MultiWriter(os.Stderr, logFile))

	// Create a channel to listen for OS signals
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	// Start a goroutine to handle the signal
	go func() {
		<-signalChan
		log.Println("Abort")
		os.Exit(1)
	}()

	ReadDirectory(rootDir, chunkSize, sleepDuration)
}
