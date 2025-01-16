package main

import (
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"
)

// ReadDirectory reads a directory using ReadDir with a chunk size and repeats the process repeatCount times.
func ReadDirectory(root string, chunkSize, repeatCount int, sleepDuration time.Duration) {
	if repeatCount <= 0 {
		fmt.Println("Repeat count must be greater than 0")
		return
	}

	fmt.Println("ReadDir stress test")
	fmt.Printf("chunkSize: %d, sleepDuration: %v\n", chunkSize, sleepDuration)
	fmt.Println()

	allStart := time.Now()

	for cycle := 1; cycle <= repeatCount; cycle++ {
		cycleStr := fmt.Sprintf("Cycle %d:", cycle)
		cycleStr = cycleStr + strings.Repeat(" ", 20 - len(cycleStr))

		currentIndex := 0
		start := time.Now()
		slept := time.Duration(0)

		var readDir func(path string)
		readDir = func(path string) {
			file, err := os.Open(path)
			if err != nil {
				fmt.Printf("Error opening directory: %v\n", err)
				return
			}
			defer file.Close()

			for {
				entries, err := file.ReadDir(chunkSize)
				if err != nil && err != fs.ErrClosed {
					fmt.Printf("Error reading directory: %v\n", err)
					return
				}

				for _, entry := range entries {
					currentIndex++
					fmt.Printf("\r%s%d...", cycleStr, currentIndex)

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
		fmt.Printf(" (%v/%v)\n", elapsed-slept, elapsed)
	}

	fmt.Println("SUCCESS", time.Since(allStart))
}

func main() {
	if len(os.Args) < 5 {
		fmt.Println("Usage: go run main.go <path> <chunkSize> <repeatCount> <sleepDurationMs>")
		return
	}

	rootDir := os.Args[1]
	chunkSize, err := strconv.Atoi(os.Args[2])
	if err != nil || chunkSize <= 0 {
		fmt.Println("Invalid chunk size. Please provide a positive integer.")
		return
	}

	repeatCount, err := strconv.Atoi(os.Args[3])
	if err != nil || repeatCount <= 0 {
		fmt.Println("Invalid repeat count. Please provide a positive integer.")
		return
	}

	sleepDurationMs, err := strconv.Atoi(os.Args[4])
	if err != nil || sleepDurationMs < 0 {
		fmt.Println("Invalid sleep duration. Please provide a non-negative integer.")
		return
	}
	sleepDuration := time.Duration(sleepDurationMs) * time.Millisecond

	ReadDirectory(rootDir, chunkSize, repeatCount, sleepDuration)
}
