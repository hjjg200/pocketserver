package main

/*
#include <stdlib.h>
#include <stdio.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/stat.h>
#include <dirent.h>
#include <errno.h>

int get_errno() {
    return errno;
}

// Fixed-signature wrapper for open.
int my_open(const char *pathname, int flags, mode_t mode) {
    return open(pathname, flags, mode);
}
*/
import "C"
import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

// ioDirEntry represents file metadata and implements fs.FileInfo and fs.DirEntry.
type ioDirEntry struct {
	FName    string      `json:"name"`
	FSize    int64       `json:"size"`
	FMode    fs.FileMode `json:"mode"`
	FModTime time.Time   `json:"modTime"`
	FIsDir   bool        `json:"isDir"`
}

// Ensure ioDirEntry implements fs.FileInfo and fs.DirEntry.
var _ fs.FileInfo = (*ioDirEntry)(nil)
var _ fs.DirEntry = (*ioDirEntry)(nil)

func (e *ioDirEntry) Name() string              { return e.FName }
func (e *ioDirEntry) Type() fs.FileMode           { return e.FMode }
func (e *ioDirEntry) IsDir() bool                 { return e.FIsDir }
func (e *ioDirEntry) Info() (fs.FileInfo, error)    { return e, nil }
func (e *ioDirEntry) Mode() fs.FileMode           { return e.FMode }
func (e *ioDirEntry) Size() int64                 { return e.FSize }
func (e *ioDirEntry) ModTime() time.Time          { return e.FModTime }
func (e *ioDirEntry) Sys() any                    { return nil } // Not used here

// ioFile wraps a C file descriptor.
type ioFile struct {
	cFd C.int
}

// ioOpenFile mimics os.OpenFile using C.open.
// It takes a path, flags, and mode, and returns an ioFile.
// Example function that mimics os.OpenFile using our fixed wrapper.
func ioOpenFile(path string, flag int, mode os.FileMode) (*os.File, error) {
    cPath := C.CString(path)
    defer C.free(unsafe.Pointer(cPath))
    m := uint32(mode)
    fd := C.my_open(cPath, C.int(flag), C.mode_t(m))
    if fd < 0 {
        return nil, syscall.Errno(C.get_errno())
    }
    // Wrap the C file descriptor into an *os.File.
    file := os.NewFile(uintptr(fd), path)
    if file == nil {
        return nil, fmt.Errorf("failed to create os.File")
    }
    return file, nil
}

// Write calls C.write on the underlying file descriptor.
func (f *ioFile) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	n := C.write(f.cFd, unsafe.Pointer(&b[0]), C.size_t(len(b)))
	if n < 0 {
		return 0, syscall.Errno(C.get_errno())
	}
	return int(n), nil
}

// Read calls C.read.
func (f *ioFile) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	n := C.read(f.cFd, unsafe.Pointer(&b[0]), C.size_t(len(b)))
	if n < 0 {
		return 0, syscall.Errno(C.get_errno())
	}
	return int(n), nil
}

// Close calls C.close.
func (f *ioFile) Close() error {
	ret := C.close(f.cFd)
	if ret != 0 {
		return syscall.Errno(C.get_errno())
	}
	return nil
}

// Seek calls C.lseek.
func (f *ioFile) Seek(offset int64, whence int) (int64, error) {
	ret := C.lseek(f.cFd, C.off_t(offset), C.int(whence))
	if ret == -1 {
		return 0, syscall.Errno(C.get_errno())
	}
	return int64(ret), nil
}

// Stat uses C.fstat to get file metadata and returns an ioDirEntry.
func (f *ioFile) Stat() (fs.FileInfo, error) {
	var stat C.struct_stat
	ret := C.fstat(f.cFd, &stat)
	if ret != 0 {
		return nil, syscall.Errno(C.get_errno())
	}
	return convertStat(&stat), nil
}

// Helper: convert C.struct_stat to *ioDirEntry.
func convertStat(stat *C.struct_stat) *ioDirEntry {
	// Use st_mtim (Linux) for modification time.
	modTime := time.Unix(int64(stat.st_mtim.tv_sec), int64(stat.st_mtim.tv_nsec))
	return &ioDirEntry{
		FName:    "", // unknown in this context
		FSize:    int64(stat.st_size),
		FMode:    fs.FileMode(stat.st_mode),
		FModTime: modTime,
		FIsDir:   (stat.st_mode & C.S_IFDIR) != 0,
	}
}

// ioReadFile mimics os.ReadFile.
// It opens the file, reads its entire content, and returns it as []byte.
func ioReadFile(path string) ([]byte, error) {
	f, err := ioOpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	buf := make([]byte, size)
	total := 0
	for total < int(size) {
		n, err := f.Read(buf[total:])
		if err != nil && err != io.EOF {
			return nil, err
		}
		if n == 0 {
			break
		}
		total += n
	}
	return buf[:total], nil
}

// ioWriteFile mimics os.WriteFile.
// It opens (or creates/truncates) the file and writes data.
func ioWriteFile(path string, data []byte) error {
	f, err := ioOpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	total := 0
	for total < len(data) {
		n, err := f.Write(data[total:])
		if err != nil {
			return err
		}
		total += n
	}
	return nil
}

// ioStat mimics os.Stat and returns an fs.DirEntry.
// It calls C.stat on the path.
func ioStat(path string) (fs.FileInfo, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	var stat C.struct_stat
	ret := C.stat(cPath, &stat)
	if ret != 0 {
		return nil, syscall.Errno(C.get_errno())
	}
	modTime := time.Unix(int64(stat.st_mtim.tv_sec), int64(stat.st_mtim.tv_nsec))
	entry := &ioDirEntry{
		FName:    path,
		FSize:    int64(stat.st_size),
		FMode:    fs.FileMode(stat.st_mode),
		FModTime: modTime,
		FIsDir:   (stat.st_mode & C.S_IFDIR) != 0,
	}
	return entry, nil
}

// ioReadDir mimics os.ReadDir.
// It uses opendir/readdir to list directory entries and returns a slice of fs.DirEntry.
func ioReadDir(path string) ([]fs.DirEntry, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	dir := C.opendir(cPath)
	if dir == nil {
		return nil, syscall.Errno(C.get_errno())
	}
	defer C.closedir(dir)

	var entries []fs.DirEntry
	for {
		// readdir returns a pointer to struct dirent.
		d := C.readdir(dir)
		if d == nil {
			break
		}
		// Convert the dirent to a Go string.
		de := (*C.struct_dirent)(unsafe.Pointer(d))
		name := C.GoString(&de.d_name[0])
		if name == "." || name == ".." {
			continue
		}
		fullPath := filepath.Join(path, name)
		// Stat the file to get metadata.
		cFullPath := C.CString(fullPath)
		var st C.struct_stat
		ret := C.stat(cFullPath, &st)
		C.free(unsafe.Pointer(cFullPath))
		if ret != 0 {
			continue
		}
		modTime := time.Unix(int64(st.st_mtim.tv_sec), int64(st.st_mtim.tv_nsec))
		entry := &ioDirEntry{
			FName:    name,
			FSize:    int64(st.st_size),
			FMode:    fs.FileMode(st.st_mode),
			FModTime: modTime,
			FIsDir:   (st.st_mode & C.S_IFDIR) != 0,
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func main() {
	// Demonstration of our functions.
	testPath := "test.txt"
	testData := []byte("Hello from CGO file IO!")

	// Write file.
	if err := ioWriteFile(testPath, testData); err != nil {
		fmt.Println("ioWriteFile error:", err)
		return
	}
	fmt.Println("Wrote file successfully.")

	// Read file.
	data, err := ioReadFile(testPath)
	if err != nil {
		fmt.Println("ioReadFile error:", err)
		return
	}
	fmt.Println("Read data:", string(data))

	// Stat file.
	info, err := ioStat(testPath)
	if err != nil {
		fmt.Println("ioStat error:", err)
		return
	}
	fmt.Printf("Stat: name=%s, size=%d, mode=%v, modTime=%v, isDir=%v\n",
		info.Name(), info.Size(), info.Mode(), info.ModTime(), info.IsDir())

	// Stat file.
	_, err = ioStat("./nonexistentpath.txt")
	if err != nil {
		fmt.Println("ioStat error:", err)
		fmt.Println("IsNotExist", os.IsNotExist(err))
	}

	// Read directory.
	entries, err := ioReadDir(".")
	if err != nil {
		fmt.Println("ioReadDir error:", err)
		return
	}
	fmt.Println("Directory entries:")
	for _, e := range entries {
		info, _ := e.Info()
		fmt.Printf(" - %s (size: %d, mode: %v, modTime: %v, isDir: %v)\n",
			e.Name(), info.Size(), info.Mode(), info.ModTime(), e.IsDir())
	}

	// Cleanup.
	os.Remove(testPath)
}
