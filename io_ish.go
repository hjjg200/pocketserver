// +build linux,386
// linux and 386
// build for iSH


package main

import (
	"fmt"
	"time"
	//"sync"
	"os"
	"io"
	"io/fs"
	"path/filepath"
	"syscall"
	"unsafe"
)

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

// ish 32 chunk 30 ms

const ioReadDirChunkSize = 32
const ioReadDirChunkThrottle = time.Millisecond * 30
const ioReadDirThrottle = time.Second * 10
const ioReadFileThrottle = ioReadDirThrottle
const ioReadFileCacheCap = 100
const ioReadFileCacheSizeLimit = 32768

/*
var ioReadDirMu sync.Mutex
var ioReadDirTime = make(map[string] time.Time)
var ioReadDirCacheMap = make(map[string] []ioDirEntry)
func ioReadDirDEP(path string) ([]fs.DirEntry, error) {
	ioReadDirMu.Lock()
	defer ioReadDirMu.Unlock()

	if last, ok := ioReadDirTime[path]; ok && time.Since(last) < ioReadDirThrottle {
		// Throttle read dir
		return ioConvertToFSDirEntry(ioReadDirCacheMap[path]), nil
	}
	ioReadDirTime[path] = time.Now()

	// Entries
	entries := make([]ioDirEntry, 0)

	// Open dir
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	
	//
	for {
		chunk, err := file.ReadDir(ioReadDirChunkSize)
		if err != nil && err != fs.ErrClosed && err != io.EOF {
			return nil, err
		}

		for _, entry := range chunk {
			
			newEntry := ioDirEntry{
				FName: entry.Name(),
				FMode: entry.Type(),
				FIsDir: entry.IsDir(),
			}
			info, err := entry.Info()
			if err != nil {
				logWarn("Failed to get info of", path+"/"+newEntry.Name() )
				continue
			}
			newEntry.FModTime = info.ModTime()
			newEntry.FSize = info.Size()

			entries = append(entries, newEntry)
		}

		if len(chunk) < ioReadDirChunkSize {
			// No more entries left to read
			break
		}

		time.Sleep(ioReadDirChunkThrottle)
	}

	// Save cache
	ioReadDirCacheMap[path] = entries

	return ioConvertToFSDirEntry(entries), nil

}

func ioConvertToFSDirEntry(entries []ioDirEntry) []fs.DirEntry {
    dirEntries := make([]fs.DirEntry, 0, len(entries))
    for _, e := range entries {
        dirEntries = append(dirEntries, &e)
    }
    return dirEntries
}


// Cache ReadFile
var ioReadFileCache = NewLRUCache[string, []byte](ioReadFileCacheCap, ioReadFileThrottle)
func ioReadFile(path string) ([]byte, error) {
	p, ok := ioReadFileCache.Get(path)
	if ok {
		return p, nil
	}

	p, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Check eligibility for cache
	if len(p) <= ioReadFileCacheSizeLimit {
		ioReadFileCache.Put(path, p)
	}

	return p, nil
}
*/

type ioDirEntry struct {
	FName	string		`json:"name"`
    FSize    int64       `json:"size"`
    FMode    fs.FileMode `json:"mode"`
    FModTime time.Time   `json:"modTime"`
    FIsDir   bool        `json:"isDir"`
}

// Ensure implements fs.FileInfo
var _ fs.DirEntry = (*ioDirEntry)(nil)
var _ fs.FileInfo = (*ioDirEntry)(nil)

func (e *ioDirEntry) Name() string {
	return e.FName
}
func (e *ioDirEntry) Type() fs.FileMode {
	return e.FMode
}
func (e *ioDirEntry) IsDir() bool {
	return e.FIsDir
}
func (e *ioDirEntry) Info() (fs.FileInfo, error) {
	return e, nil
}
func (e *ioDirEntry) Mode() fs.FileMode {
	return e.FMode
}
func (e *ioDirEntry) Size() int64 {
	return e.FSize
}
func (e *ioDirEntry) ModTime() time.Time {
	return e.FModTime
}
func (e *ioDirEntry) Sys() any {
	// Implement later when needed
	return nil
}












// ioFile wraps a C file descriptor.
type ioFile struct {
	cFd C.int
}

func ioFromOsFile(f *os.File) *ioFile {
	return &ioFile{C.int(f.Fd())}
}

// ioOpenFile mimics os.OpenFile using C.open.
// It takes a path, flags, and mode, and returns an ioFile.
// Example function that mimics os.OpenFile using our fixed wrapper.
func ioOpen(path string) (*ioFile, error) {
	return ioOpenFile(path, os.O_RDONLY, 0)
}
func ioOpenFile(path string, flag int, mode os.FileMode) (*ioFile, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	// Note: os.FileMode underlying type is uint32; we cast to C.mode_t.
    m := uint32(mode)
    fd := C.my_open(cPath, C.int(flag), C.mode_t(m))
	if fd < 0 {
		return nil, syscall.Errno(C.get_errno())
	}
	return &ioFile{cFd: fd}, nil
}

func (f *ioFile) Fd() uintptr {
	return uintptr(f.cFd)
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
	return _ioConvertStat(&stat), nil
}

// Helper: convert C.struct_stat to *ioDirEntry.
func _ioConvertStat(stat *C.struct_stat) *ioDirEntry {
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
func ioWriteFile(path string, data []byte, mode os.FileMode) error {
	f, err := ioOpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
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

// ioRemove mimics os.Remove by calling the C remove function.
func ioRemove(path string) error {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	// Call C.remove to delete the file (or empty directory).
	if res := C.remove(cPath); res != 0 {
		return syscall.Errno(C.get_errno())
	}
	return nil
}

// ioLstat mimics os.Lstat by calling C.lstat (which does not follow symlinks)
// and returns an fs.FileInfo (ioDirEntry) for the file.
func ioLstat(path string) (fs.FileInfo, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var stat C.struct_stat
	ret := C.lstat(cPath, &stat)
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

// ioReadlink mimics os.Readlink by calling C.readlink to retrieve the target of a symlink.
func ioReadlink(path string) (string, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	// Allocate a buffer for the link target.
	const bufSize = 4096
	buf := C.malloc(C.size_t(bufSize))
	if buf == nil {
		return "", fmt.Errorf("ioReadlink: failed to allocate buffer")
	}
	defer C.free(buf)

	// Cast buf to *C.char when calling readlink.
	n := C.readlink(cPath, (*C.char)(buf), C.size_t(bufSize))
	if n < 0 {
		return "", syscall.Errno(C.get_errno())
	}

	// readlink does not null-terminate, so use the returned length.
	return string(C.GoBytes(buf, C.int(n))), nil
}
