// +build !linux !386
// !linux or !386

package main

import (
	"os"
	"io/fs"
)

func ioReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
func ioReadDir(path string) ([]fs.DirEntry, error) {
	return os.ReadDir(path)
}