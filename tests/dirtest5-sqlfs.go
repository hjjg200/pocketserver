
package main

/*
CGO_CFLAGS="-D_FILE_OFFSET_BITS=64 -Dpread64=pread -Dpwrite64=pwrite -Doff64_t=off_t -fno-stack-protector" \
CGO_LDFLAGS="-fno-stack-protector" CC=i686-linux-musl-gcc CGO_ENABLED=1 GOOS=linux GOARCH=386 \
go build -o bin/uploads/dirtest5-sqlfs tests/dirtest5-sqlfs.go
*/

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
	"log"
	"io"
	"io/fs"
	"path/filepath"
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

// ioDirEntry represents file metadata and implements fs.FileInfo and fs.DirEntry.
type ioDirEntry struct {
	FName    string      `json:"name"`
	FSize    int64       `json:"size"`
	FMode    fs.FileMode `json:"mode"`
	FModTime time.Time   `json:"modTime"`
	FIsDir   bool        `json:"isDir"`
}

// Ensure ioDirEntry implements fs.DirEntry and fs.FileInfo.
var _ fs.DirEntry = (*ioDirEntry)(nil)
var _ fs.FileInfo = (*ioDirEntry)(nil)

func (e *ioDirEntry) Name() string         { return e.FName }
func (e *ioDirEntry) Type() fs.FileMode      { return e.FMode }
func (e *ioDirEntry) IsDir() bool            { return e.FIsDir }
func (e *ioDirEntry) Info() (fs.FileInfo, error) { return e, nil }
func (e *ioDirEntry) Mode() fs.FileMode      { return e.FMode }
func (e *ioDirEntry) Size() int64            { return e.FSize }
func (e *ioDirEntry) ModTime() time.Time     { return e.FModTime }
func (e *ioDirEntry) Sys() any               { return nil } // Not used here

// db is the global SQLite connection.
var db *sql.DB

// initDB opens (or creates) the SQLite database and sets up the files table.
func initDB(dbPath string) error {
	var err error
	fmt.Println("SQL init...")
	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	fmt.Println("SQL init Open")
	// Create the table if it doesn't exist.
	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS files (
		path TEXT PRIMARY KEY,
		data BLOB,
		size INTEGER,
		mode INTEGER,
		modtime INTEGER
	)`)
	fmt.Println("SQL init Exec")
	return err
}

// ioWriteFile writes data to the file identified by path.
// It uses an UPSERT: if a record for path exists, it is updated.
func ioWriteFile(path string, data []byte) error {
	// Use mode 0644 (as assumed to match the database file's mode)
	mode := os.FileMode(0644)
	modtime := time.Now().Unix()
	size := int64(len(data))
	_, err := db.Exec(`
		INSERT INTO files (path, data, size, mode, modtime)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			data=excluded.data,
			size=excluded.size,
			mode=excluded.mode,
			modtime=excluded.modtime
	`, path, data, size, int64(mode), modtime)
	return err
}

// ioReadFile reads and returns the entire file data stored at path.
func ioReadFile(path string) ([]byte, error) {
	row := db.QueryRow("SELECT data FROM files WHERE path = ?", path)
	var blob []byte
	err := row.Scan(&blob)
	return blob, err
}

// ioStat returns file metadata for the record at path as an *ioDirEntry.
func ioStat(path string) (fs.FileInfo, error) {
	row := db.QueryRow("SELECT size, mode, modtime FROM files WHERE path = ?", path)
	var size int64
	var modeInt int64
	var modtimeUnix int64
	err := row.Scan(&size, &modeInt, &modtimeUnix)
	if err != nil {
		return nil, err
	}
	entry := &ioDirEntry{
		FName:    path,
		FSize:    size,
		FMode:    fs.FileMode(modeInt),
		FModTime: time.Unix(modtimeUnix, 0),
		FIsDir:   false,
	}
	return entry, nil
}

func ioRemove(path string) error {
	result, err := db.Exec("DELETE FROM files WHERE path = ?", path)
	if err != nil {
		return fmt.Errorf("ioRemove: failed to delete %q: %w", path, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("ioRemove: failed to get affected rows for %q: %w", path, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("ioRemove: no record found for %q", path)
	}
	return nil
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

	// Initialize the database.
	dbPath := filepath.Base(os.Args[0]) + "-db.dat"
	if err := initDB(dbPath); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

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
	log.SetOutput(io.MultiWriter(logFile, os.Stderr))

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
				defer func() {
					if r := recover(); r != nil {
						log.Printf("RECOVERED Goroutine %d: %v", id, r)
					}
				}()
				defer wg.Done()

				filename := filepath.Join("temp", fmt.Sprintf("%s-%d.dat", uuidPrefix, id))
				fmt.Fprintf(log.Writer(), "_%d ", id)

				// Write file.
				if err := ioWriteFile(filename, data); err != nil {
					log.Fatalf("Goroutine %d: WriteFile error: %v\n", id, err)
				}
				fmt.Fprintf(log.Writer(),"a%d ", id)
				time.Sleep(currentSleep)

				// Stat file.
				if _, err := ioStat(filename); err != nil {
					log.Printf("Goroutine %d: Stat error: %v\n", id, err)
					return
				}
				fmt.Fprintf(log.Writer(),"b%d ", id)
				time.Sleep(currentSleep)

				// Read file.
				if b, err := ioReadFile(filename); err != nil {
					log.Printf("Goroutine %d: ReadFile error: %v\n", id, err)
					return
				} else if len(b) != fileSizeBytes {
					log.Printf("Goroutine %d: Read unexpected file size: got %d bytes\n", id, len(b))
				}
				fmt.Fprintf(log.Writer(),"c%d ", id)
				time.Sleep(currentSleep)

				// Remove file.
				if err = ioRemove(filename); err != nil {
					log.Fatalf("Goroutine %d: Remove failed: %w\n", id, err)
				}
				fmt.Fprintf(log.Writer(), "d%d ", id)
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
