package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
	"log"
	"path/filepath"
	"runtime"
	"io/fs"
	"unsafe"
)

/*
#include <stdlib.h>
#include <stdio.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/stat.h>
#include <string.h>

// Reads the entire file content
int CReadFile(const char* path, char** buffer, size_t* size) {
    FILE *file = fopen(path, "rb");
    if (!file) return -1;

    fseek(file, 0, SEEK_END);
    *size = ftell(file);
    rewind(file);

    *buffer = (char*)malloc(*size);
    if (!*buffer) {
        fclose(file);
        return -2;
    }

    fread(*buffer, 1, *size, file);
    fclose(file);
    return 0;
}

// Writes content to a file with CREATE, TRUNC, WRONLY
int CWriteFile(const char* path, const char* data, size_t size) {
    int fd = open(path, O_CREAT | O_TRUNC | O_WRONLY, 0644);
    if (fd == -1) return -1;

    if (write(fd, data, size) != size) {
        close(fd);
        return -2;
    }

    close(fd);
    return 0;
}

// Retrieves file metadata
int CStat(const char* path, struct stat* st) {
    return stat(path, st);
}
*/
import "C"

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
func (e *ioDirEntry) Type() fs.FileMode         { return e.FMode }
func (e *ioDirEntry) IsDir() bool               { return e.FIsDir }
func (e *ioDirEntry) Info() (fs.FileInfo, error) { return e, nil }
func (e *ioDirEntry) Mode() fs.FileMode         { return e.FMode }
func (e *ioDirEntry) Size() int64               { return e.FSize }
func (e *ioDirEntry) ModTime() time.Time        { return e.FModTime }
func (e *ioDirEntry) Sys() any                  { return nil } // Not used here

// CReadFile reads a file and returns its content as []byte.
func CReadFile(path string) ([]byte, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var buffer *C.char
	var size C.size_t

	res := C.CReadFile(cPath, &buffer, &size)
	if res != 0 {
		return nil, fmt.Errorf("failed to read file: %s", path)
	}
	defer C.free(unsafe.Pointer(buffer))

	// Convert C buffer to Go byte slice
	return C.GoBytes(unsafe.Pointer(buffer), C.int(size)), nil
}

// CWriteFile writes data to the given file path.
func CWriteFile(path string, data []byte) error {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cData := C.CString(string(data))
	defer C.free(unsafe.Pointer(cData))

	res := C.CWriteFile(cPath, cData, C.size_t(len(data)))
	if res != 0 {
		return fmt.Errorf("failed to write file: %s", path)
	}
	return nil
}

// CStat retrieves file metadata.
func CStat(path string) (*ioDirEntry, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var stat C.struct_stat
	res := C.CStat(cPath, &stat)
	if res != 0 {
		return nil, fmt.Errorf("failed to stat file: %s", path)
	}

	// On some Linux targets (e.g. 32-bit), st_mtime may not exist.
	// Instead, use st_mtim.tv_sec.
	modTime := int64(stat.st_mtim.tv_sec)

	entry := &ioDirEntry{
		FName:    path,
		FSize:    int64(stat.st_size),
		FMode:    fs.FileMode(stat.st_mode),
		FModTime: time.Unix(modTime, 0),
		FIsDir:   (stat.st_mode & C.S_IFDIR) != 0,
	}
	return entry, nil
}

func printUsage() {
	fmt.Printf(`Usage: %s <concurrent_count> <sleep_duration_ms> <sleep_duration_step_ms> <file_size_bytes>

  concurrent_count       : number of concurrent IO routines (must be > 0)
  sleep_duration_ms      : sleep duration (in milliseconds) between each OS function call and main loop (>= 0)
  sleep_duration_step_ms : amount to decrease sleep every 100 loops (>= 0)
  file_size_bytes        : size (in bytes) of the temporary file to write (must be > 0)

Example: %s 10 50 10 4096
`, os.Args[0], os.Args[0])
}

func main() {

	runtime.GOMAXPROCS(1)

	// Validate command-line arguments
	if len(os.Args) != 5 {
		printUsage()
		os.Exit(1)
	}

	concurrentCount, err := strconv.Atoi(os.Args[1])
	if err != nil || concurrentCount <= 0 {
		fmt.Println("Error: concurrent_count must be an integer > 0")
		printUsage()
		os.Exit(1)
	}

	sleepDurationMs, err := strconv.Atoi(os.Args[2])
	if err != nil || sleepDurationMs < 0 {
		fmt.Println("Error: sleep_duration_ms must be an integer >= 0")
		printUsage()
		os.Exit(1)
	}

	sleepStepMs, err := strconv.Atoi(os.Args[3])
	if err != nil || sleepStepMs < 0 {
		fmt.Println("Error: sleep_duration_step_ms must be an integer >= 0")
		printUsage()
		os.Exit(1)
	}

	fileSizeBytes, err := strconv.Atoi(os.Args[4])
	if err != nil || fileSizeBytes <= 0 {
		fmt.Println("Error: file_size_bytes must be an integer > 0")
		printUsage()
		os.Exit(1)
	}

	//
	//"github.com/google/uuid"
	//uuidPrefix := uuid.New().String() // iSH idk why but freezes
	uuidPrefix := fmt.Sprintf("hjjg200-dirtest4-%d", time.Now().UnixMilli())

	// Open a log file
	logFile, err := os.OpenFile(
		filepath.Base(os.Args[0])+".log",
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalln("Failed to open log file: %v", err)
	}
	defer logFile.Close()
	log.SetFlags(0)
	log.SetOutput(os.Stderr)
    err = syscall.Dup2(int(logFile.Fd()), int(os.Stderr.Fd()))
    if err != nil {
        log.Fatalf("Failed to redirect stderr to file: %v", err)
    }

	//
	log.Printf("STARTED NEW TEST\n\n")

	// Set initial sleep duration as a time.Duration.
	currentSleep := time.Duration(sleepDurationMs) * time.Millisecond

	// Setup signal handling for Ctrl-C.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("CTRL-C")
		os.Exit(0)
	}()

	loopCount := 0
	zeroSleepLoops := 0 // counts loops when currentSleep == 0

	// Pre-create a buffer of zeros for writing.
	data := make([]byte, fileSizeBytes)

	for {
		loopCount++
		// Mark start time for the loop (for OS calls only).
		loopStart := time.Now()

		var wg sync.WaitGroup
		done := make(chan struct{})
		for i := 0; i < concurrentCount; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				filename := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d.dat", uuidPrefix, id))
				fmt.Fprintf(log.Writer(), "_%d ", id)

				// Write file.
				if err := CWriteFile(filename, data); err != nil {
					log.Fatalf("Goroutine %d: WriteFile error: %v\n", id, err)
				}
				fmt.Fprintf(log.Writer(),"c%d ", id)
				time.Sleep(currentSleep)

				// Stat file.
				if _, err := CStat(filename); err != nil {
					log.Printf("Goroutine %d: Stat error: %v\n", id, err)
					return
				}
				time.Sleep(currentSleep)

				// NOTE: os.ReadFile internally does successive Open Stat without delay which causes IO freeze
				// Read file.
				if b, err := CReadFile(filename); err != nil {
					log.Printf("Goroutine %d: ReadFile error: %v\n", id, err)
					return
				} else if len(b) != fileSizeBytes {
					log.Printf("Goroutine %d: Read unexpected file size: got %d bytes\n", id, len(b))
				}

				time.Sleep(currentSleep)

				// Remove file.
				if err = os.Remove(filename); err != nil {
					log.Fatalf("Goroutine %d: Remove failed: %w\n", id, err)
				}
				fmt.Fprintf(log.Writer(), "g%d ", id)
			}(i)
		}

		// Each goroutine did 3 sleeps (they ran concurrently, so we assume the sleep overhead is currentSleep*3).
		expectedSleep := 3 * currentSleep

		go func() {
			wg.Wait()
			close(done)
		}()
		// Use `select` to wait for either `wg.Wait()` or a timeout
		select {
		case <-done:
			// ok
		case <-time.After(3*time.Second + expectedSleep):
			// NOTE: it never reaches here even though the goroutine hangs, the whole app just freezes
			//   and attempt to kill -9 the frozen background process force exits the iSH app
			//   so you cannot make a separate process that does IO handling and recreate when it freezes
			//   for killing a frozen one would render iSH exit
			log.Fatalln("Timeout! Some goroutines are still running")
		}
		loopEnd := time.Now()

		// Calculate total loop time.
		totalLoopDuration := loopEnd.Sub(loopStart)
		overhead := totalLoopDuration - expectedSleep
		if overhead < 0 {
			overhead = 0
		}

		// Print loop info: "<loop_count % 100>/100 - <sleep_duration_ms>ms --- <overhead_ms>ms"
		log.Printf("\n%d/100 - %d files: %dms --- %.2fms\n", (loopCount-1)%100+1, concurrentCount, currentSleep.Milliseconds(), overhead.Seconds()*1000)

		// Sleep between main loops.
		time.Sleep(currentSleep)

		// Every 100 loops, decrease sleep duration.
		if loopCount%100 == 0 {
			newSleep := currentSleep - time.Duration(sleepStepMs)*time.Millisecond
			if newSleep < 0 {
				newSleep = 0
			}
			currentSleep = newSleep

			// When sleep is 0, count loops; after 100 such loops, exit successfully.
			if currentSleep == 0 {
				zeroSleepLoops++
				if zeroSleepLoops >= 100 {
					log.Println("Completed 100 loops with 0ms sleep. Exiting with SUCCESS.")
					os.Exit(0)
				}
			} else {
				// Reset zeroSleepLoops if sleep > 0.
				zeroSleepLoops = 0
			}
		}
	}
}
