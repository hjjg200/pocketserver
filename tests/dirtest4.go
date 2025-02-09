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
	"io/ioutil"
	"path/filepath"
	"runtime"
)

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
	logFile, err := ioOpenFile(
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

				/* NOTE: CreateTemp makes retries without delay which might cause freeze of iSH io
				// Create a unique temporary file.
				tmpFile, err := os.CreateTemp("", "io_test_*.tmp")
				if err != nil {
					log.Printf("Goroutine %d: CreateTemp error: %v\n", id, err)
					return
				}
				filename := tmpFile.Name()
				tmpFile.Close()*/

				filename := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d.dat", uuidPrefix, id))
				fmt.Fprintf(log.Writer(), "_%d ", id)

				//
				f, err := ioOpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
				if err != nil {
					log.Fatalf("Goroutine %d: OpenFile error: %v\n", id, err)
				}
				fmt.Fprintf(log.Writer(), "a%d ", id)
				err = f.Close()
				if err != nil {
					log.Fatalf("Goroutine %d: Close failed: %w\n", id, err)
				}
				fmt.Fprintf(log.Writer(), "b%d ", id)

				time.Sleep(currentSleep)

				// Write file.
				if err := os.WriteFile(filename, data, 0644); err != nil {
					log.Fatalf("Goroutine %d: WriteFile error: %v\n", id, err)
				}
				fmt.Fprintf(log.Writer(),"c%d ", id)
				time.Sleep(currentSleep)

				// Stat file.
				/*
				if _, err := os.Stat(filename); err != nil {
					log.Printf("Goroutine %d: Stat error: %v\n", id, err)
					return
				}
				time.Sleep(currentSleep)*/

				// NOTE: os.ReadFile internally does successive Open Stat without delay which causes IO freeze
				/*
				// Read file.
				if b, err := os.ReadFile(filename); err != nil {
					log.Printf("Goroutine %d: ReadFile error: %v\n", id, err)
					return
				} else if len(b) != fileSizeBytes {
					log.Printf("Goroutine %d: Read unexpected file size: got %d bytes\n", id, len(b))
				}*/
				f, err = os.Open(filename)
				if err != nil {
					log.Fatalf("Goroutine %d: Open error: %v\n", id, err)
				}
				fmt.Fprintf(log.Writer(), "d%d ", id)
				p, err := ioutil.ReadAll(f)
				if err != nil || len(p) != fileSizeBytes {
					log.Printf("Goroutine %d: Read unexpected file size: got %d bytes\n", id, len(p))
				}
				fmt.Fprintf(log.Writer(), "e%d ", id)
				err = f.Close()
				if err != nil {
					log.Fatalf("Goroutine %d: Close failed: %w\n", id, err)
				}
				fmt.Fprintf(log.Writer(), "f%d ", id)

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
