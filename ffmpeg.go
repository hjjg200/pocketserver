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

    "github.com/gorilla/websocket"
)

const FFMPEG_PREFIX = "[FFmpeg]"


func initFFmpeg() {
	arg0 := filepath.Base(os.Args[0])

	// Check if the program is invoked as "ffmpeg" or the main app
	if (len(os.Args) > 1 &&
		arg0 == "ffmpeg" ||
		arg0 == "pocketserver.ffmpeg" ||
		arg0 == "ffprobe" ||
		arg0 == "pocketserver.ffprobe") {
		subFFmpeg(os.Args)
		os.Exit(0)
	} else {
		ffmpegHandler = makeFFmpegHandler()
	}
}



type FFmpegPipeTask struct {
	FFargsJson string
	LogLineCh chan string
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
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("error getting cwd: %v", err)
	}

	res := &FFmpegArgs{
		Cwd:    cwd,
		Inputs: []int{},
		Outputs: []int{},
		Args:   args,
	}

	lastWasDashI := false

	for i := 0; i < len(args); i++ {
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
    if _, err := os.Stat(socketPath); err == nil {
        os.Remove(socketPath)
    }

    listener, err := net.Listen("unix", socketPath)
    if err != nil {
        logFatal(FFMPEG_PREFIX, "Socket listen error:", err)
    }

    logInfo(FFMPEG_PREFIX, "Main FFmpeg worker is listening on UNIX socket", socketPath)

    // Channel used to pass “args” from the Unix socket connections
    // to the WebSocket connections.
    ch := make(chan FFmpegPipeTask)

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
				scanner := bufio.NewScanner(conn)
				for scanner.Scan() {
					ffargsJson := scanner.Text()
					logDebug(FFMPEG_PREFIX, "Received json of arguments:", ffargsJson)

					logLineCh := make(chan string)
					ch <-FFmpegPipeTask{
						ffargsJson, logLineCh,
					}

					// Wait for the task's log
					for logLine := range logLineCh {
						fmt.Fprintln(conn, logLine)
					}
					return
				}
			
				if err := scanner.Err(); err != nil {
					logInfo(FFMPEG_PREFIX, "Scanner error:", err)
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
        logInfo(FFMPEG_PREFIX, "WebSocket request using gorilla")

        // Use the gorilla Upgrader to turn this into a WebSocket
        wsConn, err := upgrader.Upgrade(w, r, nil)
        if err != nil {
            logInfo(FFMPEG_PREFIX, "Upgrade error:", err)
            return
        }
        defer wsConn.Close()

        logInfo(FFMPEG_PREFIX, "WebSocket connection established (gorilla)")

        // Read messages in a loop from the browser
        for {
			// TODO error handling for the browser side

            // msgType will be websocket.TextMessage or Binary, etc.
            _, msg, err := wsConn.ReadMessage()
            if err != nil {
                logError(FFMPEG_PREFIX, "Websocket read error:", err)
                return
            }
            line := strings.TrimSpace(string(msg))
            logDebug(FFMPEG_PREFIX, "Received:", line)

            if line == "ready" {
                logDebug(FFMPEG_PREFIX, "Browser is ready")
                // Wait for an "args" string from the channel
                pipeTask := <-ch
				ffargsJson := pipeTask.FFargsJson

				// Parse json for processing on this end
				var ffargs FFmpegArgs
				if err = json.Unmarshal([]byte(ffargsJson), &ffargs); err != nil {
					logError(FFMPEG_PREFIX, "FFmpeg args json error:", err)
					return
				}

                // Send back to the browser
                err = wsConn.WriteMessage(websocket.TextMessage, []byte(ffargsJson))
                if err != nil {
                    logError(FFMPEG_PREFIX, "Write error:", err)
                    return
                }
				
				// Write inputs to the wasm end
				if err = processFFmpegInputs(wsConn, ffargs); err != nil {
					logError(FFMPEG_PREFIX, "Failed to write input files to websocket:", err)
					return
				}

				for {
					typ, logLineObj, err := parseFFmpegTextMessage(wsConn.ReadMessage())
					if err != nil {
						logError(FFMPEG_PREFIX, "Reading logline, Websocket read error:", err)
						return
					}
					if typ == "logLine" {
						pipeTask.LogLineCh <-logLineObj["logLine"].(string)
					} else if typ == "logEnd" {
						// logLine is now over
						break
					} else {
						logError(FFMPEG_PREFIX, "Reading logLine, unexpected message", logLineObj)
						return
					}
				}

				if err = processFFmpegOutputs(wsConn, ffargs); err != nil {
					logError(FFMPEG_PREFIX, "Failed to process output files:", err)
					return
				}
				logDebug(FFMPEG_PREFIX, "Success")

				// Send close to close unix socket
				close(pipeTask.LogLineCh)
            }
        }
    }
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
			return fmt.Errorf("Reading logline, Websocket read error: %w", err)
		}
		if typ != "outInfo" {
			return fmt.Errorf("Malformed outInfo packet %v", outInfoObj)
		}

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
		out, err := os.Create(outPath)
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
		info, err := os.Stat(inPath)
		if err != nil {
			return fmt.Errorf("Failed to stat input %s: %w", inPath, err)
		}

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

		// Wait for ok
		typ, _, err := parseFFmpegTextMessage(wsConn.ReadMessage())
		if err != nil {
			return fmt.Errorf("Reading info ok, Websocket read error: %w", err)
		}
		if typ != "inputInfoOk" {
			return fmt.Errorf("Wrong order of operation no input info ok")
		}

		// Stream input file
		in, err := os.Open(inPath)
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
func subFFmpeg(args []string) {

	socketPath := filepath.Join(os.TempDir(), "pocketserver.ffmpeg.sock")

	// Connect to the UNIX socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		logFatal(FFMPEG_PREFIX, "Dial error:", err)
	}
	defer conn.Close()

	// Send arguments to the main worker
	ffargs, err := parseFFmpegArgs(args)
	if err != nil {
		logFatal(FFMPEG_PREFIX, "Parse argument error:", err)
	}

	ffargsJson, err := json.Marshal(ffargs)
	if err != nil {
		logFatal(FFMPEG_PREFIX, "Failed to marshal json", err)
	}
	fmt.Fprintln(conn, string(ffargsJson))
	//logInfo(FFMPEG_PREFIX, "SPAWNED pocketserver_ish SUBORDINATE WORKER FOR PROCESSING", string(ffargsJson))

	// Read response from the main worker
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		response := scanner.Text()
		fmt.Println(response)
	}

	if err := scanner.Err(); err != nil {
		logFatal(FFMPEG_PREFIX, "Scanner error:", err)
	}
}
