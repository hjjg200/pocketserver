// +build linux,386
// linux and 386
// build for iSH


package main

import (
	"fmt"
	"time"
	//"sync"
	"errors"
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
#include <poll.h>

int get_errno() {
    return errno;
}

// Fixed-signature wrapper for open.
int my_open(const char *pathname, int flags, mode_t mode) {
    return open(pathname, flags, mode);
}

// waitFD waits for the file descriptor fd to become ready for the given events (e.g. POLLIN, POLLOUT)
// within the timeout (in milliseconds). It loops on EINTR.
int waitFD(int fd, short events, int timeout) {
    struct pollfd pfd;
    pfd.fd = fd;
    pfd.events = events;
    int ret;
    do {
        ret = poll(&pfd, 1, timeout);
    } while(ret == -1 && errno == EINTR);
    return ret;
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

func ioCastFileMode(stMode C.mode_t) fs.FileMode {
    // Extract the lower 9 permission bits.
    mode := fs.FileMode(uint32(stMode) & 0777)

    // Isolate the file type bits using C.S_IFMT and compare against C constants.
    switch stMode & C.S_IFMT {
    case C.S_IFDIR:
        mode |= fs.ModeDir
    case C.S_IFLNK:
        mode |= fs.ModeSymlink
    case C.S_IFIFO:
        mode |= fs.ModeNamedPipe
    case C.S_IFSOCK:
        mode |= fs.ModeSocket
    case C.S_IFCHR:
        mode |= fs.ModeDevice | fs.ModeCharDevice
    case C.S_IFBLK:
        mode |= fs.ModeDevice
    }
    return mode
}











// ioFile wraps a C file descriptor.
type ioFile struct {
	name string
	cFd C.int
}

var ioErrTimeout = errors.New("Timeout")
func ioIsTimeout(err error) bool {
	return errors.Is(err, ioErrTimeout)
}

const IO_RETRY_INTERVAL = 10 * time.Millisecond
const IO_RETRY_COUNT = 10
const IO_EAGAIN_TIMEOUT_MS = 50

var ioStdout = &ioFile{os.Stdout.Name(), C.STDOUT_FILENO}
var ioStderr = &ioFile{os.Stderr.Name(), C.STDERR_FILENO}

// waitForFD uses the C.waitFD helper to wait for a file descriptor to be ready.
// events should be C.POLLIN for read readiness or C.POLLOUT for write readiness.
func _ioWaitFd(cFd C.int, events int16) error {
	// Wait for fd to be ready indefinitely
    ret := C.waitFD(cFd, C.short(events), C.int(IO_EAGAIN_TIMEOUT_MS))
    if ret == 0 {
        return ioErrTimeout
    } else if ret < 0 {
        return syscall.Errno(C.get_errno())
    }
    return nil
}
func _ioWaitRead(cFd C.int) error {
	return _ioWaitFd(cFd, C.POLLIN)
}
func _ioWaitWrite(cFd C.int) error {
	return _ioWaitFd(cFd, C.POLLOUT)
}


func ioFromOsFile(f *os.File) *ioFile {
	return &ioFile{f.Name()/*Name() immediately returns f.name*/, C.int(f.Fd())}
}

// ioOpenFile mimics os.OpenFile using C.open.
// It takes a path, flags, and mode, and returns an ioFile.
// Example function that mimics os.OpenFile using our fixed wrapper.
func ioOpen(path string) (*ioFile, error) {
	return ioOpenFile(path, os.O_RDONLY, 0)
}
func ioOpenFile(path string, flag int, mode os.FileMode) (*ioFile, error) {
    for i := 0; i < IO_RETRY_COUNT; i++ {
		f, err := ioIgnoringEINTR2(func() (*ioFile, error) {
			cPath := C.CString(path)
			defer C.free(unsafe.Pointer(cPath))
			m := uint32(mode)
			fd := C.my_open(cPath, C.int(flag), C.mode_t(m))
			if fd < 0 {
				return nil, syscall.Errno(C.get_errno())
			}
			return &ioFile{path, fd}, nil
		})

		if err == syscall.EAGAIN {
			time.Sleep(IO_RETRY_INTERVAL)
			continue
		}

		return f, err
	}
	return nil, fmt.Errorf("Failed to open file: %s err: %w", path, ioErrTimeout)
}


func (f *ioFile) Fd() uintptr {
	return uintptr(f.cFd)
}

// Write calls C.write on the underlying file descriptor.
func (f *ioFile) Write(b []byte) (int, error) {
    if len(b) == 0 {
        return 0, nil
    }
    for i := 0; i < IO_RETRY_COUNT; i++ {
		k, err := ioIgnoringEINTR2(func() (int, error) {
			n := C.write(f.cFd, unsafe.Pointer(&b[0]), C.size_t(len(b)))
			if n < 0 {
				return 0, syscall.Errno(C.get_errno())
			}
			return int(n), nil
		})

		if err == syscall.EAGAIN {
			err = _ioWaitWrite(f.cFd)
			if ioIsTimeout(err) {
				continue
			}
		}
		return k, err
	}

	return 0, fmt.Errorf("Failed to write to file: %s err: %w", f.name, ioErrTimeout)
}


// Read calls C.read.
func (f *ioFile) Read(b []byte) (int, error) {
    if len(b) == 0 {
        return 0, nil
    }
    for i := 0; i < IO_RETRY_COUNT; i++ {
		k, err := ioIgnoringEINTR2(func() (int, error) {
			n := C.read(f.cFd, unsafe.Pointer(&b[0]), C.size_t(len(b)))
			if n < 0 {
				return 0, syscall.Errno(C.get_errno())
			}
			return int(n), nil
		})
		
		if err == syscall.EAGAIN {
			err = _ioWaitRead(f.cFd)
			if ioIsTimeout(err) {
				continue
			}
		}
		return k, err
	}

	return 0, fmt.Errorf("Failed to read file: %s err: %w", f.name, ioErrTimeout)
}


// Close calls C.close.
// https://man7.org/linux/man-pages/man2/close.2.html
func (f *ioFile) Close() error {
	return ioIgnoringEINTR(func() error {
		ret := C.close(f.cFd)
		if ret != 0 {
			return syscall.Errno(C.get_errno())
		}
		return nil
	})
}

// Seek calls C.lseek.
func (f *ioFile) Seek(offset int64, whence int) (int64, error) {
    for i := 0; i < IO_RETRY_COUNT; i++ {
		k, err := ioIgnoringEINTR2(func() (int64, error) {
			ret := C.lseek(f.cFd, C.off_t(offset), C.int(whence))
			if ret == -1 {
				return 0, syscall.Errno(C.get_errno())
			}
			return int64(ret), nil
		})

		if err == syscall.EAGAIN {
			err = _ioWaitRead(f.cFd)
			if ioIsTimeout(err) {
				continue
			}
		}

		return k, err
	}
	return 0, fmt.Errorf("Failed to seek file: %s err: %w", f.name, ioErrTimeout)
}


// Stat uses C.fstat to get file metadata and returns an ioDirEntry.
func (f *ioFile) Stat() (fs.FileInfo, error) {
    for i := 0; i < IO_RETRY_COUNT; i++ {
		info, err := ioIgnoringEINTR2(func() (fs.FileInfo, error) {
			var stat C.struct_stat
			ret := C.fstat(f.cFd, &stat)
			if ret != 0 {
				return nil, syscall.Errno(C.get_errno())
			}
			return _ioConvertStat(f.name, &stat), nil
		})

		if err == syscall.EAGAIN {
			err = _ioWaitRead(f.cFd)
			if ioIsTimeout(err) {
				continue
			}
		}

		return info, err
	}
	return nil, fmt.Errorf("Failed to stat file: %s err: %w", f.name, ioErrTimeout)
}


// Helper: convert C.struct_stat to *ioDirEntry.
func _ioConvertStat(name string, stat *C.struct_stat) *ioDirEntry {
	// Use st_mtim (Linux) for modification time.
	modTime := time.Unix(int64(stat.st_mtim.tv_sec), int64(stat.st_mtim.tv_nsec))
	mode := ioCastFileMode(stat.st_mode)
	return &ioDirEntry{
		FName:    name,
		FSize:    int64(stat.st_size),
		FMode:    mode,
		FModTime: modTime,
		FIsDir:   mode.IsDir(),
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

// readdirWrapper calls C.readdir repeatedly, ignoring EINTR and printing a message if EAGAIN is encountered.
func _ioReaddirWrapper(dir *C.DIR) (*C.struct_dirent, error) {
    for i := 0; i < IO_RETRY_COUNT; i++ {
		k, err := ioIgnoringEINTR2(func() (*C.struct_dirent, error) {
			d := C.readdir(dir)
			if d == nil {
				// Check errno if readdir returns nil.
				errno := C.get_errno()
				if errno != 0 {
					return nil, syscall.Errno(errno)
				}
				// If errno is zero, it's the end of the directory.
			}
			return d, nil
		})

		if err == syscall.EAGAIN {
			err = _ioWaitRead(C.dirfd(dir))
			if ioIsTimeout(err) {
				continue
			}
		}

		return k, err
	}

	return nil, ioErrTimeout
}

func ioReadDir(path string) ([]fs.DirEntry, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	
	dir := C.opendir(cPath)
	if dir == nil {
		return nil, syscall.Errno(C.get_errno())
	}
	defer C.closedir(dir)
	
	var entries []fs.DirEntry
	count := 0
	for {
		d, err := _ioReaddirWrapper(dir)
		if err != nil {
			if ioIsTimeout(err) {
				continue
			}
			return nil, err
		}
		// d == nil indicates end-of-directory.
		if d == nil {
			break
		}
		// Convert the returned struct_dirent pointer.
		de := (*C.struct_dirent)(unsafe.Pointer(d))
		name := C.GoString(&de.d_name[0])
		if name == "." || name == ".." {
			continue
		}

		// valid dirent
		count++
		fullPath := filepath.Join(path, name)
		cFullPath := C.CString(fullPath)
		var st C.struct_stat
		ret := C.stat(cFullPath, &st)
		C.free(unsafe.Pointer(cFullPath))
		if ret != 0 {
			// Skip this entry if stat fails.
			continue
		}
		entry := _ioConvertStat(name, &st)
		entries = append(entries, entry)
	}
	
	logDebug("Readdir:", count, "files in", path)
	return entries, nil
}


// ioRemove mimics os.Remove by calling the C remove function.
func ioRemove(path string) error {
	return ioIgnoringEINTR(func() error {
		cPath := C.CString(path)
		defer C.free(unsafe.Pointer(cPath))

		// Call C.remove to delete the file (or empty directory).
		if res := C.remove(cPath); res != 0 {
			return syscall.Errno(C.get_errno())
		}
		return nil
	})
}



// ioStat mimics os.Stat and returns an fs.DirEntry.
// It calls C.stat on the path.
func ioStat(path string) (fs.FileInfo, error) {
    for i := 0; i < IO_RETRY_COUNT; i++ {
		info, err := ioIgnoringEINTR2(func() (fs.FileInfo, error) {
			cPath := C.CString(path)
			defer C.free(unsafe.Pointer(cPath))

			var stat C.struct_stat
			ret := C.stat(cPath, &stat)
			if ret != 0 {
				return nil, syscall.Errno(C.get_errno())
			}

			entry := _ioConvertStat(path, &stat)
			return entry, nil
		})

		if err == syscall.EAGAIN {
			time.Sleep(IO_RETRY_INTERVAL)
			continue
		}

		return info, err
	}
	return nil, fmt.Errorf("Failed to stat path: %s err: %w", path, ioErrTimeout)
}

// ioLstat mimics os.Lstat by calling C.lstat (which does not follow symlinks)
// and returns an fs.FileInfo (ioDirEntry) for the file.
func ioLstat(path string) (fs.FileInfo, error) {
    for i := 0; i < IO_RETRY_COUNT; i++ {
		info, err := ioIgnoringEINTR2(func() (fs.FileInfo, error) {
			cPath := C.CString(path)
			defer C.free(unsafe.Pointer(cPath))

			var stat C.struct_stat
			ret := C.lstat(cPath, &stat)
			if ret != 0 {
				return nil, syscall.Errno(C.get_errno())
			}
			
			// Create the ioDirEntry with the correctly set mode.
			entry := _ioConvertStat(path, &stat)
			return entry, nil
		})

		if err == syscall.EAGAIN {
			time.Sleep(IO_RETRY_INTERVAL)
			continue
		}

		return info, err
	}
	return nil, fmt.Errorf("Failed to lstat path: %s err: %w", path, ioErrTimeout)
}

// ioReadlink mimics os.Readlink by calling C.readlink to retrieve the target of a symlink.
func ioReadlink(path string) (string, error) {
	return ioIgnoringEINTR2(func() (string, error) {
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
	})
}


// ioPipe mimics the Pipe function by calling C.pipe2 with the O_CLOEXEC flag.
// It returns two *ioFile objects, one for reading and one for writing.
func ioPipe() (r *ioFile, w *ioFile, err error) {
    var fds [2]C.int
    err = ioIgnoringEINTR(func() error {
        if ret := C.pipe2(&fds[0], C.int(syscall.O_CLOEXEC)); ret != 0 {
            return syscall.Errno(C.get_errno())
        }
        return nil
    })
    if err != nil {
        return nil, nil, err
    }
    return &ioFile{"|0", fds[0]}, &ioFile{"|1", fds[1]}, nil
}


// ignoringEINTR makes a function call and repeats it if it returns an
// EINTR error. This appears to be required even though we install all
// signal handlers with SA_RESTART: see #22838, #38033, #38836, #40846.
// Also #20400 and #36644 are issues in which a signal handler is
// installed without setting SA_RESTART. None of these are the common case,
// but there are enough of them that it seems that we can't avoid
// an EINTR loop.
func ioIgnoringEINTR(fn func() error) error {
    for {
        err := fn()
        if err != syscall.EINTR {
            return err
        }
    }
}

func ioIgnoringEINTR2[T any](fn func() (T, error)) (T, error) {
    for {
        v, err := fn()
        if err != syscall.EINTR {
            return v, err
        }
    }
}