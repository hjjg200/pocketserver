package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"io"
	"encoding/json"
	"path/filepath"
	"net/http"
	"mime"
	"strings"
	"errors"
	"time"
	"sync"
	"sync/atomic"

    "github.com/gorilla/websocket"
)

const FFMPEG_PREFIX = "[FFmpeg]"


func checkRunAsFFmpeg() {
	arg0	:= filepath.Base(os.Args[0])
	arg0	= strings.TrimSuffix(arg0, filepath.Ext(arg0))

	// Check if the program is invoked as "ffmpeg" or the main app
	if (arg0 == "ffmpeg" || arg0 == "ffprobe") {

		// Attempt to do websocket
		err := subFFmpeg(os.Args)
		if err != nil {
			// If failed go for native
			err = executeFFmpeg(os.Args, ioStdout, ioStderr)
			if err != nil {
				logFatal(err)
			}
		}

		os.Exit(0)
	}
}

func initFFmpegSocket() {
	ffmpegHandler = makeFFmpegHandler()
}

var ffmpegSempahore = NewSemaphore(PERF_FFMPEG_MAX_CONCURRENT, 0)
// Find the native ffmpeg and run it
func executeFFmpeg(args []string, stdout, stderr *ioFile) (error) {

	ffmpegSempahore.Acquire()
	defer ffmpegSempahore.Release()
	defer logDebug2('f', "d", 10)

	logDebug2('f', 10)
	arg0 := filepath.Base(args[0])
	arg0 = strings.TrimSuffix(arg0, filepath.Ext(arg0))

	nativeFFs, err := findFFmpegInPath()
	logDebug2('f', 20)
	native, ok := nativeFFs[arg0]
	if !ok || err != nil {
		return fmt.Errorf("No native ffmpeg is found err: %w", err)
	}
	args[0] = native
	if arg0 == "ffmpeg" {

		// ffprobe doesn't receive -y, use -y for ffmpeg only
		args = append([]string{args[0], "-y"}, args[1:]...)

	} else if arg0 == "ffprobe" {

		// Handle -o option for older version of ffprobe that doesn't have -o (iSH one doesn't)
		var outputPath string
		newArgs := make([]string, 0, len(args))

		for i := 0; i < len(args); i++ {
			if args[i] == "-o" {
				// Ensure there is a following argument
				if i+1 >= len(args) {
					return fmt.Errorf("No output path provided for ffprobe: %v", args)
				}
				outputPath = args[i+1] // Get the output path
				i++ // Skip next arg since it's the output file
			} else {
				newArgs = append(newArgs, args[i])
			}
		}

		// if output option is found
		if outputPath != "" {
			// Priortize over the given stdout
			logDebug2('f', 25)
			out, err := ioOpenFile(outputPath, os.O_CREATE | os.O_TRUNC | os.O_WRONLY, 0644)
			if err != nil {
				return fmt.Errorf("Failed to create output file for ffprobe: %s %w", outputPath, err)
			}
			stdout = out
			defer out.Close()
			args = newArgs
		}

	}

	// ---
	wait, _, err := _executeFFmpeg(args, stdout, stderr)
	if err != nil {
		return fmt.Errorf("Failed to start ffmpeg process: %w", err)
	}

	<-wait
	
	logDebug2('f', 50)
	return nil

}

func findFFmpegInPath() (map[string]string, error) {

	// Get the executable path
	pocketExecPath, err := os.Executable()
	must(err)
	pocketExecPath = resolveSymlink(pocketExecPath)

	logDebug2('f', 10)

	stems := []string{"ffmpeg", "ffprobe"}

	// Get the PATH environment variable
	pathEnv := os.Getenv("PATH")
	logDebug2('f', 20)
	if pathEnv == "" {
		return nil, fmt.Errorf("$PATH is empty")
	}

	logDebug2('f', 30)

	// Split PATH into directories
	pathDirs := filepath.SplitList(pathEnv)

	// Create a set for quick lookup of basenames
	stemSet := make(map[string]struct{})
	for _, stem := range stems {
		stemSet[stem] = struct{}{}
	}

	// Map to store the first matches
	matches := make(map[string]string)

	// Search each directory in PATH
	for _, dir := range pathDirs {
		files, err := ioReadDir(dir)
		logDebug2('f', 40, dir, len(files), err)
		if err != nil {
			continue // Skip directories that cannot be read
		}

		for _, file := range files {
			if file.IsDir() {
				continue // Skip directories
			}

			// Get the file name
			base := file.Name()

			// Get the file basename (exclude extension)
			stem := strings.TrimSuffix(base, filepath.Ext(base))

			// Check if it matches any of the requested basenames
			if _, found := stemSet[stem]; found {
				// Ensure it's executable and not already matched
				fullpath := filepath.Join(dir, base)

				// Follow symlink
				fullpath = resolveSymlink(fullpath)

				// Check it is not pocketserver
				if fullpath == pocketExecPath {
					continue
				}

				if _, exists := matches[stem]; !exists {
					matches[stem] = fullpath
				}
			}
		}
	}

	return matches, nil
}



type FFmpegPipeTask struct {
	FFargsJson string
	LogLineCh chan string
	WsConn atomic.Pointer[websocket.Conn]
}


type FFmpegArgs struct {
	Cwd		string		`json:"cwd"`
	Inputs	[]int		`json:"inputs"`
	Outputs	[]int		`json:"outputs"`
	Args 	[]string	`json:"args"`
}

// parseFFmpegArgs does a single pass:
//   - If an arg starts with '-' => flag. If it equals "-i", the next file is an input
//   - If an arg starts with "file:", remove "file:" prefix
//   - Use mime.TypeByExtension to check if extension is recognized
//   - If recognized or not, if preceded by "-i", => input; else => output
func parseFFmpegArgs(args []string) (*FFmpegArgs, error) {

	logDebug2('f', 10)
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("error getting cwd: %w", err)
	}

	res := &FFmpegArgs{
		Cwd:    cwd,
		Inputs: []int{},
		Outputs: []int{},
		Args:   args,
	}

	lastWasDashI := false

	// Ignore 0th argument
	logDebug2('f', 20)
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			// It's a flag
			if arg == "-i" {
				lastWasDashI = true
			} else {
				lastWasDashI = false
			}
			continue
		}

		// Not a flag => treat as potential file path
		const prefix = "file:"
		path := arg
		if strings.HasPrefix(path, prefix) {
			// remove "file:"
			path = path[len(prefix):]
			args[i] = path
		}

		ext := strings.ToLower(filepath.Ext(path))
		mimeType := mime.TypeByExtension(ext)

		// Ignore args that don't seem like a path
		if mimeType == "" {
			continue
		}

		if lastWasDashI {
			// Preceded by -i => input
			res.Inputs = append(res.Inputs, i)
			lastWasDashI = false
		} else {
			// Not preceded by -i => output
			res.Outputs = append(res.Outputs, i)
		}
	}

	return res, nil
}















var ffmpegHandler func(w http.ResponseWriter, r *http.Request)




func makeFFmpegHandler() http.HandlerFunc {
    socketPath := filepath.Join(os.TempDir(), "pocketserver.ffmpeg.sock")

    // Clean up existing socket
    if _, err := ioStat(socketPath); err == nil {
        ioRemove(socketPath)
    }

    listener, err := net.Listen("unix", socketPath)
    if err != nil {
        logFatal(FFMPEG_PREFIX, "Socket listen error:", err)
    }

    logInfo(FFMPEG_PREFIX, "Main FFmpeg worker is listening on UNIX socket", socketPath)

    // Channel used to pass “args” from the Unix socket connections
    // to the WebSocket connections.
    ch := make(chan *FFmpegPipeTask, 10)

    // Goroutine to accept Unix socket connections
	go func() {
		defer listener.Close()

		for {
			c, err := listener.Accept()
			if err != nil {
				logWarn(FFMPEG_PREFIX, "Accept error:", err)
				continue
			}
	
			// Handle incoming connections in the main worker
			go func(conn net.Conn) {
				defer conn.Close()
			
				// Read input from the subordinate
				reader := bufio.NewReader(conn)
				streamType, msgLen, err := readSimplePayloadHeader(reader)
				if streamType != "ffargsJson" {
					logError(FFMPEG_PREFIX, "Wrong protocol for ffargs:", streamType)
					return
				}
				payload := make([]byte, msgLen)
				_, err = io.ReadFull(reader, payload)
				if err != nil {
					logError(FFMPEG_PREFIX, "failed to read payload:", err)
					return
				}
			
				ffargsJson := string(payload)
				logDebug(FFMPEG_PREFIX, "UNIX CONN, Received json of arguments:", ffargsJson)

				subAbort := make(chan struct{})
				subAborted := false
				go func() {
					// Any read from this point indicates close
					p := make([]byte, 1)
					conn.Read(p)
					subAborted = true
					subAbort <-struct{}{}
				}()

				RetryLoop:
				for {

					logLineCh := make(chan string)
					pipeTask := &FFmpegPipeTask{
						FFargsJson: ffargsJson, LogLineCh: logLineCh,
					}
					ch <-pipeTask
					logDebug(FFMPEG_PREFIX, "Queued pipeTask")

					logLines := []string{}
					// Wait for the task's log
					// TODO use select for separated channels for logging and signal
					SelectLoop:
					for {
						select {
						case <-subAbort:
							wsConn := pipeTask.WsConn.Load()
							if wsConn != nil {
								wsConn.Close()
							}
	
						case logLine, ok := <-logLineCh:

							if !ok {
								break SelectLoop
							}
	
							switch logLine {
							case FFMPEG_WS_SOCKET_CLOSED:
								close(logLineCh)
								if subAborted {
									logDebug(FFMPEG_PREFIX, "Subordinate worker aborted")
									break		RetryLoop
								} else {
									logDebug(FFMPEG_PREFIX, "Websocket client aborted the job reseting stdout, stderr history")
									continue	RetryLoop
								}
							case FFMPEG_WS_SERVER_FAILED:
								logDebug(FFMPEG_PREFIX, "Websocket server failed to process the task!")
								close(logLineCh)
								break		RetryLoop
							}
	
							logLines = append(logLines, logLine)
	
						}
					}
					
					// Finished task send via unix
					for _, logLine := range logLines {
						fmt.Fprint(conn, logLine)
					}
					break RetryLoop

				}

			}(c)
		}

	}()

	var upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

    // Return an HTTP handler (for /ws/ffmpeg) that uses gorilla/websocket
    return func(w http.ResponseWriter, r *http.Request) {
		logHTTPRequest(r, -1, FFMPEG_PREFIX, "ffmpeg websocket established")

        // Use the gorilla Upgrader to turn this into a WebSocket
        wsConn, err := upgrader.Upgrade(w, r, nil)
        if err != nil {
			logHTTPRequest(r, 599, FFMPEG_PREFIX, "Upgrade error:", err)
            return
        }
        defer wsConn.Close()

		var mu sync.Mutex

        // Read messages in a loop from the browser
        for {
			
			err = pingPongFFmpegMessageOfType(wsConn, "ready", nil)
			if err != nil {
				logHTTPRequest(r, 599, FFMPEG_PREFIX, "websocket failed to get ready", err)
				return
			}

			logDebug(FFMPEG_PREFIX, "Browser is ready and is waiting")

			// Wait for a job or client abort
			clientAbort := make(chan struct{})
			taskReady := false
			go func() {
				for {
					time.Sleep(5*time.Second)
					mu.Lock()
					if taskReady {
						mu.Unlock()
						return
					}
					err = pingPongFFmpegMessageOfType(wsConn, "wait", nil)
					if err != nil {
						clientAbort <-struct{}{}
					}
					mu.Unlock()
				}
			}()
			var pipeTask *FFmpegPipeTask
			select {
			case v := <-ch:
				pipeTask = v
				mu.Lock()
				taskReady = true
				mu.Unlock()
			case <-clientAbort:
				logHTTPRequest(r, 599, FFMPEG_PREFIX, "Websocket closed:", err)
				return
			}
			
			// ---
			pipeTask.WsConn.Store(wsConn) // store conn for abort handling

			err = pingPongFFmpegMessageOfType(wsConn, "taskReady", nil)
			if err != nil {
				// Hand out fftask to another
				logHTTPRequest(r, 399, FFMPEG_PREFIX, "websocket failed handing task out to another", err)
				ch <-pipeTask
				return
			}

			logHTTPRequest(r, -1, FFMPEG_PREFIX, "received ffargs json", pipeTask.FFargsJson)

			var wsErr *websocket.CloseError
			var opErr *net.OpError
			err = processFFmpegTask(wsConn, pipeTask)
			if errors.As(err, &wsErr) ||
				errors.As(err, &opErr) ||
				errors.Is(err, net.ErrClosed) {
				pipeTask.LogLineCh <-FFMPEG_WS_SOCKET_CLOSED
				logHTTPRequest(r, 599, FFMPEG_PREFIX, "Websocket closed:", err)
				return
			} else if err != nil {
				pipeTask.LogLineCh <-FFMPEG_WS_SERVER_FAILED
				logHTTPRequest(r, 599, FFMPEG_PREFIX, "Processing pipe task failed:", err)
				return
			}

			close(pipeTask.LogLineCh)
			// x99 is to just color the log
			logHTTPRequest(r, 299, FFMPEG_PREFIX, "Successful ffmpeg task")

        }
    }
}


func processFFmpegTask(wsConn *websocket.Conn, pipeTask *FFmpegPipeTask) error {

	// Parse json for processing on this end
	var ffargs FFmpegArgs
	if err := json.Unmarshal([]byte(pipeTask.FFargsJson), &ffargs); err != nil {
		return fmt.Errorf("FFmpeg args json error: %w", err)
	}

	// Send ffargs
	err := pingPongFFmpegMessageOfType(wsConn, "ffargs", ffargs)
	if err != nil {
		return fmt.Errorf("Failed to ping pong ffargs: %w", err)
	}
	
	// Write inputs to the wasm end
	if err = processFFmpegInputs(wsConn, ffargs); err != nil {
		return fmt.Errorf( "Failed to write input files to websocket: %w", err)
	}

	for {
		typ, logLineObj, err := parseFFmpegTextMessage(wsConn.ReadMessage())
		if err != nil {
			return fmt.Errorf("Reading logline, Websocket read error: %w", err)
		}
		if typ == "logLine" {

			logLine := logLineObj["logLine"].(string)
			packet := fmt.Sprintf("%s %d\n%s",
				logLineObj["logType"].(string),
				len(logLine),
				logLine,
			)
			pipeTask.LogLineCh <-packet

		} else if typ == "logEnd" {
			// logLine is now over
			break
		} else {
			return fmt.Errorf("Reading logLine, unexpected message: %v", logLineObj)
		}
	}

	if err = processFFmpegOutputs(wsConn, ffargs); err != nil {
		return fmt.Errorf("Failed to process output files: %w", err)
	}
	
	return nil

}

func pingPongFFmpegMessageOfType(wsConn *websocket.Conn, typ string, val interface{}) error {

	err := writeFFmpegMessageOfType(wsConn, typ, val)
	if err != nil {
		return err
	}
	err = readFFmpegMessageOfType(wsConn, typ)
	if err != nil {
		return err
	}

	return nil

}

func writeFFmpegMessageOfType(wsConn *websocket.Conn, typ string, val interface{}) error {

	data := make(map[string]interface{})
	data["type"] = typ
	if val != nil {
		data[typ] = val
	}
	msg, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("Json marshal error %w: %w", err, data)
	}
	err = wsConn.WriteMessage(websocket.TextMessage, msg)
	if err != nil {
		return fmt.Errorf("Write error: %w", err)
	}
	return nil

}

func readFFmpegMessageOfType(wsConn *websocket.Conn, typ string) error {

	typ1, _, err := parseFFmpegTextMessage(wsConn.ReadMessage())
	if err != nil {
		return fmt.Errorf("Reading "+typ+", Websocket read error: %w", err)
	}
	if typ1 != typ {
		return fmt.Errorf("Reading "+typ+", wrong message %v", typ1)
	}

	return nil

}

func parseFFmpegTextMessage(msgType int, msg []byte, err error) (string, map[string]interface{}, error) {
	
	if err != nil {
		return "", nil, err
	}

	if msgType != websocket.TextMessage {
		return "", nil, fmt.Errorf("Not a text message")
	}

	var m map[string]interface{}

	err = json.Unmarshal(msg, &m)
	if err != nil {
		return "", nil, err
	}

	typ, ok := m["type"].(string)
	if !ok {
		return "", nil, fmt.Errorf("Type not found for ffmpeg message")
	}

	return typ, m, nil

}

func formatFFmpegArgPath(ffargs FFmpegArgs, i int) string {
	p := ffargs.Args[i]
	if filepath.IsAbs(p) == false {
		p = filepath.Join(ffargs.Cwd, p)
	}
	return p
}

func processFFmpegOutputs(wsConn *websocket.Conn, ffargs FFmpegArgs) error {

	for _, outIndex := range ffargs.Outputs {
		
		typ, outInfoObj, err := parseFFmpegTextMessage(wsConn.ReadMessage())
		if err != nil {
			return fmt.Errorf("Reading outInfo, Websocket read error: %w", err)
		}
		if typ != "outInfo" {
			return fmt.Errorf("Malformed outInfo packet %v", outInfoObj)
		}

		logDebug(FFMPEG_PREFIX, "outInfo", outIndex)

		// Read output metadata
		outInfoIface, ok := outInfoObj["outInfo"].([]interface{})
		if !ok {
			return fmt.Errorf("Wrong output metadata object %v: ffargs: %v", outInfoObj, ffargs)
		}
		outInfo := make([]int64, 2)
		for i, iface := range outInfoIface {
			f, ok := iface.(float64)
			if !ok {
				return fmt.Errorf("Wrong output metadata object %v ffargs: %v", outInfoObj, ffargs)
			}
			outInfo[i] = int64(f)
		}

		// Check if it is the correct index
		if int64(outIndex) != outInfo[0] {
			return fmt.Errorf("Malformed outInfo, wrong out index for %d: %v", outIndex, outInfoObj)
		}

		logDebug(FFMPEG_PREFIX, "outIndex", outIndex, "size", outInfo[1])

		// Write output
		outPath := formatFFmpegArgPath(ffargs, outIndex)
		out, err := ioOpenFile(outPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return fmt.Errorf("Failed to create output file: %s err: %w", outPath, err)
		}
		defer out.Close()

		// Copy all data from the WebSocket connection to the file
		msgType, wsRd, err := wsConn.NextReader()
		if err != nil {
			return fmt.Errorf("Failed to make websocket reader: %w", err)
		}
		if msgType != websocket.BinaryMessage {
			return fmt.Errorf("Malformed data type from websocket: %d", msgType)
		}
		n, err := io.Copy(out, wsRd)
		if err != nil {
			return fmt.Errorf("Failed to read and write to output: %s err: %w", outPath, err)
		}
		if n != outInfo[1] {
			return fmt.Errorf("Size mismatch for output file %d, %d", n, outInfo[1])
		}
		// TODO checksum
		logDebug(FFMPEG_PREFIX, "Successfully", formatBytes(n), "written as output:", outPath)
	}
	return nil

}

func processFFmpegInputs(wsConn *websocket.Conn, ffargs FFmpegArgs) error {

	for _, inputIndex := range ffargs.Inputs {

		// Stat file
		inPath := formatFFmpegArgPath(ffargs, inputIndex)
		info, err := ioStat(inPath)
		if err != nil {
			return fmt.Errorf("Failed to stat input %s: %w", inPath, err)
		}

		logDebug(FFMPEG_PREFIX, "stat", inputIndex, inPath)

		// Write the current input's index
		infoJson, err := json.Marshal([]int64{
			int64(inputIndex), info.Size(),
		})
		if err != nil {
			return fmt.Errorf("Failed to marshal: %w", err)
		}
		err = wsConn.WriteMessage(websocket.TextMessage, infoJson)
		if err != nil {
			return fmt.Errorf("Failed to write to websocket [1]: %w", err)
		}

		logDebug(FFMPEG_PREFIX, "written", inputIndex)

		// Wait for ok
		typ, _, err := parseFFmpegTextMessage(wsConn.ReadMessage())
		if err != nil {
			return fmt.Errorf("Reading info ok, Websocket read error: %w", err)
		}
		if typ != "inputInfoOk" {
			return fmt.Errorf("Wrong order of operation no input info ok")
		}

		logDebug(FFMPEG_PREFIX, "ok sent", inputIndex)

		// Stream input file
		in, err := ioOpen(inPath)
		if err != nil {
			return fmt.Errorf("Failed to open input file %s: %w", inPath, err)
		}
		defer in.Close()
		wsWr, err := wsConn.NextWriter(websocket.BinaryMessage)
		if err != nil {
			return fmt.Errorf("Failed to create writer: %w", err)
		}
		n, err := io.Copy(wsWr, in)
		if err != nil {
			return fmt.Errorf("Failed to write to websocket [2]: %w", err)
		}
		err = wsWr.Close() // Close() flushes the written message
		if err != nil {
			return fmt.Errorf("Failed to flush the mssage: %w", err)
		}
		
		logDebug(FFMPEG_PREFIX, "input sent", inputIndex)

		// Wait for ok
		typ, _, err = parseFFmpegTextMessage(wsConn.ReadMessage())
		if err != nil {
			return fmt.Errorf("Reading input ok, Websocket read error: %w", err)
		}
		if typ != "inputOk" {
			return fmt.Errorf("Wrong order of operation no input ok")
		}

		logDebug(FFMPEG_PREFIX, inPath, formatBytes(n), "written to websocket")

	}

	return nil

}




// Subordinate worker: Sends commands to the main worker
func subFFmpeg(args []string) error {

	// Parse arguments
	logDebug2('f', 10)
	ffargs, err := parseFFmpegArgs(args)
	if err != nil {
		return fmt.Errorf("Parse argument error: %w", err)
	}
	logDebug2('f', 20)

	// If run as ffprobe and there is only output it means it is input
	if strings.HasSuffix(args[0], "ffprobe") &&
		len(ffargs.Inputs) == 0 &&
		len(ffargs.Outputs) > 0 {
		ffargs.Inputs, ffargs.Outputs = ffargs.Outputs, ffargs.Inputs
	}

	// Send arguments to the main worker
	ffargsJson, err := json.Marshal(ffargs)
	if err != nil {
		return fmt.Errorf("Failed to marshal json: %w", err)
	}

	// DO THE WORK VIA WEBSOCKET
	// Connect to the UNIX socket
	logDebug2('f', 30)
	socketPath := filepath.Join(os.TempDir(), "pocketserver.ffmpeg.sock")
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("Dial error: %w", err)
	}
	logDebug2('f', 40)
	defer conn.Close()

	fmt.Fprint(conn, fmt.Sprintf("%s %d\n%s", "ffargsJson", len(ffargsJson), ffargsJson))
	//logInfo(FFMPEG_PREFIX, "SPAWNED pocketserver_ish SUBORDINATE WORKER FOR PROCESSING", string(ffargsJson))

	// Read response from the main worker
	reader := bufio.NewReader(conn)
	for {
        streamType, msgLen, err := readSimplePayloadHeader(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to read header: %w", err)
		}

        // 3) read exactly msgLen bytes
        payload := make([]byte, msgLen)
        _, err = io.ReadFull(reader, payload)
        if err != nil {
			return fmt.Errorf("failed to read payload: %w", err)
        }

        // 4) Output to stdout or stderr
        switch streamType {
        case "stdout":
			fmt.Fprintln(ioStdout, string(payload))
        case "stderr":
			fmt.Fprintln(ioStderr, string(payload))
        default:
            // Unknown stream type, decide what to do
			return fmt.Errorf("Unknown stream type: %v", streamType)
            // we still have the data in 'payload' if needed
        }
    }

	return nil

}
