// +build linux,386
// linux and 386
// build for iSH


package main

import (
	"time"
	"sync"
	"os"
	"io/fs"
)

// ish 32 chunk 30 ms

const ioReadDirChunkSize = 32
const ioReadDirChunkThrottle = time.Millisecond * 30
const ioReadDirThrottle = time.Second * 10
const ioReadFileThrottle = ioReadDirThrottle
const ioReadFileCacheCap = 100
const ioReadFileCacheSizeLimit = 32768

var ioReadDirMu sync.Mutex
var ioReadDirTime = make(map[string] time.Time)
var ioReadDirCacheMap = make(map[string] []ioDirEntry)
func ioReadDir(path string) ([]fs.DirEntry, error) {
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
		if err != nil && err != fs.ErrClosed {
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