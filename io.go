package main

import (
	//"fmt"
	//"io"
	//"time"
	"errors"
)

var ioErrTimeout = errors.New("Timeout")
func ioIsTimeout(err error) bool {
	return errors.Is(err, ioErrTimeout)
}

/*
// TimeoutPipe wraps an os.Pipe with timeout detection and redirection.
type ioTimeoutPipe struct {
	r           *ioFile       // Read end of the pipe.
	w           *ioFile       // Write end of the pipe.
	redirect    io.Writer      // Destination for data read from the pipe.
	timeout     time.Duration  // Timeout period.
}
// makeTimeoutPipe creates a pipe that reads output, writes it to the redirect writer,
// and sends a timeout signal if no data is read within `timeout`. It returns:
//  - timeoutChan (read-only) → Receives a signal when a timeout occurs.
//  - writer (write-end of the pipe) → Where data should be written.
func ioTimeoutWriter(redirect io.Writer, timeout time.Duration) (w *ioFile, err error) {
	r, w, err := ioPipe()
	if err != nil {
		return nil, err
	}

	tp := &ioTimeoutPipe{
		r:           r,
		w:           w,
		redirect:    redirect,
		timeout:     timeout,
	}

	return w, nil
}

// monitor continuously reads from the pipe and writes to the redirect writer.
// If no data is read for `tp.timeout`, a timeout signal is sent.
func (tp *ioTimeoutPipe) Copy() error {

	buf := make([]byte, 1024)

	for {
		// Start a timeout timer
		timeout := time.After(tp.timeout)

		// Read data from the pipe
		n, err := func() (int, error) {
			// Use a select to either read data or trigger a timeout
			readDone := make(chan struct{})
			var bytesRead int
			var readErr error

			go func() {
				bytesRead, readErr = tp.r.Read(buf)
				close(readDone)
			}()

			select {
			case <-timeout:
				// If timeout occurs before reading data, send timeout signal.
				go func() {
					tp.timeoutChan <- struct{}{}
				}()
				return 0, ioErrTimeout
			case <-readDone:
				// Data was read, return results.
				return bytesRead, readErr
			}
		}()

		// Handle read error
		if err != nil {
			if err == io.EOF {
				return io.EOF
			}
			return fmt.Errorf("Read error: %w", err)
		}

		// Write data to redirect writer
		if n > 0 && tp.redirect != nil {
			_, werr := tp.redirect.Write(buf[:n])
			if werr != nil {
				return fmt.Errorf("Failed write to redirect: %w", werr)
			}
		}
	}
	err := tp.r.Close()
	if err != nil {
		return err
	}
	return tp.w.Close()
}

*/