package main

import (
	"bufio"
	"fmt"
	"flag"
	"net"
	"os"
	"io"
	"encoding/json"
	"path/filepath"
	"net/http"
	"strings"

    "github.com/gorilla/websocket"
)

const FFMPEG_PREFIX = "[FFmpeg]"


func initFFmpeg() {
	arg0 := filepath.Base(os.Args[0])

	// Check if the program is invoked as "ffmpeg" or the main app
	if (arg0 == "ffmpeg" || arg0 == "pocketserver.ffmpeg") && len(os.Args) > 1 {
		subFFmpeg(os.Args)
		os.Exit(0)
	} else {
		ffmpegHandler = makeFFmpegHandler()
	}
}



type FFmpegPipeTask struct {
	FFargsJson string
	Done chan []byte
}


type FFmpegArgs struct {
	Cwd		string		`json:"cwd"`
	Inputs	[]int		`json:"inputs"`
	Output	int			`json:"output"`
	Args 	[]string	`json:"args"`
}

// parseFFmpegArgs parses ffmpeg-style arguments and returns indexes for inputs and output.
func parseFFmpegArgs(args []string) (*FFmpegArgs, error) {

	// Get cwd
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("Error getting current working directory: %v", err)
	}

	// Initialize variables for input and output indexes
	var inputIndexes []int
	outputIndex := -1

	// Custom flag parsing
	fs := flag.NewFlagSet("ffmpeg", flag.ContinueOnError)
	outputPtr := fs.String("o", "", "Output file (optional)")

	// Parse all flags to allow for `-o` or similar custom flags
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	// Loop over remaining args to find `-i` flags and the output file
	remainingArgs := fs.Args()
	for i := 0; i < len(remainingArgs); i++ {
		if remainingArgs[i] == "-i" {
			if i+1 >= len(remainingArgs) {
				return nil, fmt.Errorf("missing input file after -i")
			}
			inputIndexes = append(inputIndexes, i+1)
			i++ // Skip the next argument (input file)
		} else if strings.HasPrefix(remainingArgs[i], "-") {
			// Skip flags
			continue
		} else {
			// Assume the last non-flag argument is the output file if not set by `-o`
			outputIndex = i
		}
	}

	// If `-o` is used, it takes precedence for the output file
	if *outputPtr != "" {
		for idx, arg := range args {
			if arg == *outputPtr {
				outputIndex = idx
				break
			}
		}
	}

	// Validate parsed inputs and output
	if len(inputIndexes) == 0 {
		return nil, fmt.Errorf("no input files provided")
	}
	if outputIndex == -1 {
		return nil, fmt.Errorf("no output file provided")
	}

	return &FFmpegArgs{
		Cwd:	cwd,
		Inputs:	inputIndexes,
		Output:	outputIndex,
		Args:	args,
	}, nil
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

					done := make(chan []byte)
					ch <-FFmpegPipeTask{
						ffargsJson, done,
					}

					// Wait for the task to be done and send done via socket
					ffmpegLogsJson := <-done

					var ffmpegLogs struct{
						Logs []string `json:"logs"`
					}
					err = json.Unmarshal(ffmpegLogsJson, &ffmpegLogs)
					if err != nil {
						logError(FFMPEG_PREFIX, "Failed to unmarshal ffmpeg logs object", err)
						return
					}
					
					for _, line := range ffmpegLogs.Logs {
						fmt.Fprintln(conn, line)
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

				// Check files
				if ffargs.Output == -1 || len(ffargs.Inputs) == 0 {
					logError(FFMPEG_PREFIX, "Malformed ffmpeg args:", ffargs)
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

				// Read output metadata
				msgType, msg, err := wsConn.ReadMessage()
				if err != nil {
					logError(FFMPEG_PREFIX, "Reading output metadata, Websocket read error:", err)
					return
				}
				if msgType != websocket.TextMessage {
					logError(FFMPEG_PREFIX, "Reading output metadata, not text message")
					return
				}
				var outInfo []int64
				if err = json.Unmarshal(msg, &outInfo); err != nil {
					logError(FFMPEG_PREFIX, "Failed to unmarshal output metadata:", err)
					return
				}
				if outInfo[0] != int64(ffargs.Output) {
					logError(FFMPEG_PREFIX, "Wrong output metadata", outInfo, "ffargs:", ffargs)
					return
				}

				// Write output
				outPath := formatFFmpegArgPath(ffargs, ffargs.Output)
				out, err := os.Create(outPath)
				if err != nil {
					logError(FFMPEG_PREFIX, "Failed to create output file:", outPath, "err:", err)
					return
				}
				defer out.Close()

				// Copy all data from the WebSocket connection to the file
				msgType, wsRd, err := wsConn.NextReader()
				if err != nil {
					logError(FFMPEG_PREFIX, "Failed to make websocket reader:", err)
					return
				}
				if msgType != websocket.BinaryMessage {
					logError(FFMPEG_PREFIX, "Malformed data type from websocket:", msgType)
					return
				}
				n, err := io.Copy(out, wsRd)
				if err != nil {
					logError(FFMPEG_PREFIX, "Failed to read and write to output:", outPath, "err:", err)
					return
				}
				if n != outInfo[1] {
					logError(FFMPEG_PREFIX, "Size mismatch for output file", n, outInfo[1])
					return
				}
				// TODO checksum
				logDebug(FFMPEG_PREFIX, "Successfully", formatBytes(n), "written as output:", outPath)

				// Read the logs
				msgType, ffmpegLogsJson, err := wsConn.ReadMessage()
				if err != nil {
					logError(FFMPEG_PREFIX, "Reading ffmpeg logs, Websocket read error:", err)
					return
				}
				if msgType != websocket.TextMessage {
					logError(FFMPEG_PREFIX, "Reading ffmpeg logs, not text message")
					return
				}

				// Finish the job
				pipeTask.Done <-ffmpegLogsJson
            }
        }
    }
}


func formatFFmpegArgPath(ffargs FFmpegArgs, i int) string {
	p := ffargs.Args[i]
	if filepath.IsAbs(p) == false {
		p = filepath.Join(ffargs.Cwd, p)
	}
	return p
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
