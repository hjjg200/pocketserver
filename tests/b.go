package main

import (
	"fmt"
	"os"
	"time"
)

// time dd if=/dev/zero of=/tmp/test1.img bs=1G count=1

func main() {
	// Configuration similar to dd
	const totalSize = 1 << 30 // 1 GiB total size
	const chunkSize = 1 << 20 // 1 MiB chunk size
	outputFile := "/tmp/test1.img"

	// Calculate the number of chunks to write
	chunks := totalSize / chunkSize

	// Create or truncate the output file
	file, err := os.Create(outputFile)
	if err != nil {
		fmt.Printf("Error creating file: %v\n", err)
		return
	}
	defer file.Close()

	// Create a smaller block of zeros
	zeroBlock := make([]byte, chunkSize)

	fmt.Printf("Writing %d MiB chunks (%d bytes) to %s...\n", chunkSize/(1<<20), totalSize, outputFile)

	start := time.Now()

	// Write the chunks to the file
	for i := int64(0); i < int64(chunks); i++ {
		_, err := file.Write(zeroBlock)
		if err != nil {
			fmt.Printf("Error writing to file: %v\n", err)
			return
		}
	}

	// Write any remaining bytes (not applicable here since totalSize % chunkSize == 0)
	remaining := totalSize % chunkSize
	if remaining > 0 {
		_, err := file.Write(zeroBlock[:remaining])
		if err != nil {
			fmt.Printf("Error writing remaining bytes: %v\n", err)
			return
		}
	}

	// Ensure all data is flushed to disk
	err = file.Sync()
	if err != nil {
		fmt.Printf("Error syncing file: %v\n", err)
		return
	}

	duration := time.Since(start)
	fmt.Printf("Write completed in %v\n", duration)
}