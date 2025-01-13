package main

import (
	"fmt"
	"hash/crc32"
)

func main() {
	// Input string
	input := "SheetJS" // -1647298270

	// Calculate CRC32 checksum
	checksum := crc32.ChecksumIEEE([]byte(input))

	h := crc32.NewIEEE()
	h.Write([]byte(input))
	fmt.Println(h.Sum32())

	// Print the result
	fmt.Printf("CRC32 checksum of '%s': %d (0x%x)\n", input, checksum, checksum)
}