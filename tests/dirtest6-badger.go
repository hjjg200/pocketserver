// main.go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/dgraph-io/badger/v4"
)

// fileRecord holds the file data and metadata.
type fileRecord struct {
	Data    []byte `json:"data"`
	Size    int64  `json:"size"`
	Mode    int64  `json:"mode"`
	ModTime int64  `json:"modtime"`
}

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

// db is the global Badger database.
var db *badger.DB

// fileKey returns the key used for a given file path.
func fileKey(path string) []byte {
	return []byte("files:" + path)
}

// initDB opens (or creates) the Badger database at dbPath.
func initDB(dbPath string) error {
	opts := badger.DefaultOptions(dbPath)
	var err error
	db, err = badger.Open(opts)
	return err
}

// ioWriteFile writes data and metadata for the file identified by path.
// It stores a JSON-encoded fileRecord using an UPSERT-like strategy.
func ioWriteFile(path string, data []byte) error {
	rec := fileRecord{
		Data:    data,
		Size:    int64(len(data)),
		Mode:    int64(os.FileMode(0644)), // assuming mode 0644
		ModTime: time.Now().Unix(),
	}
	val, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("ioWriteFile: marshal error: %w", err)
	}
	err = db.Update(func(txn *badger.Txn) error {
		return txn.Set(fileKey(path), val)
	})
	return err
}

// ioReadFile reads and returns the file data stored at path.
func ioReadFile(path string) ([]byte, error) {
	var rec fileRecord
	err := db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(fileKey(path))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &rec)
		})
	})
	if err != nil {
		return nil, fmt.Errorf("ioReadFile: %w", err)
	}
	return rec.Data, nil
}

// ioStat returns file metadata as an fs.FileInfo.
func ioStat(path string) (fs.FileInfo, error) {
	var rec fileRecord
	err := db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(fileKey(path))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &rec)
		})
	})
	if err != nil {
		return nil, fmt.Errorf("ioStat: %w", err)
	}
	entry := &ioDirEntry{
		FName:    path,
		FSize:    rec.Size,
		FMode:    fs.FileMode(rec.Mode),
		FModTime: time.Unix(rec.ModTime, 0),
		FIsDir:   false,
	}
	return entry, nil
}

// ioRemove deletes the record for the file identified by path.
func ioRemove(path string) error {
	err := db.Update(func(txn *badger.Txn) error {
		return txn.Delete(fileKey(path))
	})
	if err != nil {
		return fmt.Errorf("ioRemove: failed to delete %q: %w", path, err)
	}
	return nil
}

func printUsage() {
	fmt.Printf(`Usage: %s <concurrent_count> <sleep_duration_ms> <sleep_duration_step_ms> <file_size_bytes>

  concurrent_count       : number of concurrent IO routines (must be > 0)
  sleep_duration_ms      : sleep duration (in milliseconds) between each IO function call and main loop (>= 0)
  sleep_duration_step_ms : amount to decrease sleep every 100 loops (>= 0)
  file_size_bytes        : size (in bytes) of the temporary file to write (must be > 0)

Example: %s 10 50 10 4096
`, os.Args[0], os.Args[0])
}

func main() {

	runtime.GOMAXPROCS(1)

	// Validate command-line arguments.
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

	// Initialize the Badger database in a directory based on the program name.
	dbPath := filepath.Base(os.Args[0]) + "-db"
	if err := initDB(dbPath); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Set a UUID-like prefix (using timestamp) for generating unique file names.
	uuidPrefix := fmt.Sprintf("hjjg200-dirtest4-%d", time.Now().UnixMilli())

	// Open a log file.
	logFile, err := os.OpenFile(filepath.Base(os.Args[0])+".log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	defer logFile.Close()
	log.SetFlags(0)
	log.SetOutput(io.MultiWriter(logFile, os.Stderr))

	log.Printf("STARTED NEW TEST\n\n")

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
		loopStart := time.Now()

		var wg sync.WaitGroup
		done := make(chan struct{})
		for i := 0; i < concurrentCount; i++ {
			wg.Add(1)
			go func(id int) {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("RECOVERED Goroutine %d: %v", id, r)
					}
				}()
				defer wg.Done()

				// Generate a unique "file" path.
				filename := filepath.Join("temp", fmt.Sprintf("%s-%d.dat", uuidPrefix, id))
				fmt.Fprintf(log.Writer(), "_%d ", id)

				// Write file.
				if err := ioWriteFile(filename, data); err != nil {
					log.Fatalf("Goroutine %d: WriteFile error: %v\n", id, err)
				}
				fmt.Fprintf(log.Writer(), "a%d ", id)
				time.Sleep(currentSleep)

				// Stat file.
				if _, err := ioStat(filename); err != nil {
					log.Printf("Goroutine %d: Stat error: %v\n", id, err)
					return
				}
				fmt.Fprintf(log.Writer(), "b%d ", id)
				time.Sleep(currentSleep)

				// Read file.
				if b, err := ioReadFile(filename); err != nil {
					log.Printf("Goroutine %d: ReadFile error: %v\n", id, err)
					return
				} else if len(b) != fileSizeBytes {
					log.Printf("Goroutine %d: Read unexpected file size: got %d bytes\n", id, len(b))
				}
				fmt.Fprintf(log.Writer(), "c%d ", id)
				time.Sleep(currentSleep)

				// Remove file.
				if err := ioRemove(filename); err != nil {
					log.Fatalf("Goroutine %d: Remove failed: %v\n", id, err)
				}
				fmt.Fprintf(log.Writer(), "d%d ", id)
			}(i)
		}

		expectedSleep := 3 * currentSleep

		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			// All goroutines completed.
		case <-time.After(3*time.Second + expectedSleep):
			log.Fatalln("Timeout! Some goroutines are still running")
		}
		loopEnd := time.Now()
		totalLoopDuration := loopEnd.Sub(loopStart)
		overhead := totalLoopDuration - expectedSleep
		if overhead < 0 {
			overhead = 0
		}

		log.Printf("\n%d/100 - %d files: %dms --- %.2fms\n", (loopCount-1)%100+1, concurrentCount, currentSleep.Milliseconds(), overhead.Seconds()*1000)

		time.Sleep(currentSleep)

		if loopCount%100 == 0 {
			newSleep := currentSleep - time.Duration(sleepStepMs)*time.Millisecond
			if newSleep < 0 {
				newSleep = 0
			}
			currentSleep = newSleep

			if currentSleep == 0 {
				zeroSleepLoops++
				if zeroSleepLoops >= 100 {
					log.Println("Completed 100 loops with 0ms sleep. Exiting with SUCCESS.")
					os.Exit(0)
				}
			} else {
				zeroSleepLoops = 0
			}
		}
	}
}
