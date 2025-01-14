package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: go run main.go <target-dir> <count>")
		return
	}

	targetDir := os.Args[1]
	count, err := parseCount(os.Args[2])
	if err != nil {
		fmt.Printf("Invalid count: %v\n", err)
		return
	}

	fmt.Printf("Target Directory: %s\n", targetDir)
	fmt.Printf("Iterations: %d\n", count)

	// Measure WalkDir
	walkDirTime := measureWalkDir(targetDir, count)
	fmt.Printf("WalkDir Total Time: %s\n", walkDirTime)

	// Measure ReadDir
	readDirTime := measureReadDir(targetDir, count)
	fmt.Printf("ReadDir Total Time: %s\n", readDirTime)
}

func parseCount(input string) (int, error) {
	var count int
	_, err := fmt.Sscanf(input, "%d", &count)
	return count, err
}

func measureWalkDir(targetDir string, count int) time.Duration {
	start := time.Now()
	for i := 0; i < count; i++ {
		err := filepath.WalkDir(targetDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// Process the file or directory (e.g., get the name)
			_ = d.Name()
			return nil
		})
		if err != nil {
			fmt.Printf("WalkDir encountered an error: %v\n", err)
			break
		}
	}
	return time.Since(start)
}

func measureReadDir(targetDir string, count int) time.Duration {
	start := time.Now()
	for i := 0; i < count; i++ {
		err := readDirRecursively(targetDir)
		if err != nil {
			fmt.Printf("ReadDir encountered an error: %v\n", err)
			break
		}
	}
	return time.Since(start)
}

func readDirRecursively(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		// Process the file or directory (e.g., get the name)
		_ = entry.Name()
		if entry.IsDir() {
			err = readDirRecursively(filepath.Join(dir, entry.Name()))
			if err != nil {
				return err
			}
		}
	}
	return nil
}
