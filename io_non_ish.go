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
func ioWriteFile(path string, data []byte, mode os.FileMode) error {
	return os.WriteFile(path, data, mode)
}
func ioStat(path string) (fs.FileInfo, error) {
	return os.Stat(path)
}
func ioOpen(path string) (*os.File, error) {
	return os.Open(path)
}
func ioOpenFile(path string, flag int, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag, mode)
}