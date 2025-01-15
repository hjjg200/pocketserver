package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: go run main.go <target-dir> <count>")
		return
	}

	fmt.Println(1)

	targetDir := os.Args[1]
	count, err := parseCount(os.Args[2])
	fmt.Println(2)
	if err != nil {
		fmt.Printf("Invalid count: %v\n", err)
		return
	}

	fmt.Println(3)
	fmt.Printf("Target Directory: %s\n", targetDir)
	fmt.Printf("Iterations: %d\n", count)

	// Measure ReadDir count
	fmt.Println(4)
	readDirTime := measureReadDirCount(targetDir, count)
	fmt.Printf("ReadDir counting Total Time: %s\n", readDirTime)
}

func parseCount(input string) (int, error) {
	var count int
	_, err := fmt.Sscanf(input, "%d", &count)
	return count, err
}

func measureReadDirCount(targetDir string, count int) time.Duration {
	start := time.Now()

	for i := 0; i < count; i++ {

		entries, err := os.ReadDir(targetDir)
		if err != nil {
			panic(err)
		}
		fmt.Println("count", len(entries))

	}

	return time.Since(start)
}
