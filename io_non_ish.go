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
func ioOpen(path string) (*ioFile, error) {
	return ioOpenFile(path, os.O_RDONLY, 0)
}
func ioOpenFile(path string, flag int, mode os.FileMode) (*ioFile, error) {
	f, err := os.OpenFile(path, flag, mode)
	if err != nil {
		return nil, err
	}
	return &ioFile{f}, nil
}
func ioRemove(path string) error {
	return os.Remove(path)
}
func ioLstat(path string) (fs.FileInfo, error) {
	return os.Lstat(path)
}
func ioReadlink(path string) (string, error) {
	return os.Readlink(path)
}


type ioFile struct {
	f *os.File
}

func ioFromOsFile(f *os.File) *ioFile {
	return &ioFile{f}
}
func (f *ioFile) Fd() uintptr {
	return f.f.Fd()
}
func (f *ioFile) Write(b []byte) (int, error) {
	return f.f.Write(b)
}
func (f *ioFile) Read(b []byte) (int, error) {
	return f.f.Read(b)
}
func (f *ioFile) Close() error {
	return f.f.Close()
}
func (f *ioFile) Seek(offset int64, whence int) (int64, error) {
	return f.f.Seek(offset, whence)
}
func (f *ioFile) Stat() (fs.FileInfo, error) {
	return f.f.Stat()
}
