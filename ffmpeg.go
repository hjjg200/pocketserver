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
	"sync"

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
	LogLineCh chan string
}


type FFmpegArgs struct {
	Cwd		string		`json:"cwd"`
	Inputs	[]int		`json:"inputs"`
	Output	int			`json:"output"`
	Args 	[]string	`json:"args"`
}

func parseFFmpegArgs(args []string) (*FFmpegArgs, error) {
    // 1) Resolve the current working directory
    cwd, err := os.Getwd()
    if err != nil {
        return nil, fmt.Errorf("Error getting current working directory: %v", err)
    }

    // 2) Make a copy of `args` minus any program name if needed. If your first element
    //    is "ffmpeg" or "ffmpeg2", you can skip it. Otherwise you can keep them all.
    //    For example, if the user calls "ffmpeg -i in.mov out.mp4", then args might
    //    start with "ffmpeg" at index 0. If that's extraneous, we remove it:
    // 
    //    If you want to keep the entire array, you can skip this step. It's optional.
    // 
    realArgs := args
    if len(realArgs) > 0 && (realArgs[0] == "ffmpeg" || realArgs[0] == "ffmpeg2") {
        realArgs = realArgs[1:] // drop the program name
    }

    // 3) We'll parse known flags, if you like. For example, if you accept `-o output`,
    //    or other custom flags, you can do so with flag.NewFlagSet. Otherwise skip.
    fs := flag.NewFlagSet("ffmpeg", flag.ContinueOnError)
    outputPtr := fs.String("o", "", "Output file (optional)")
    // Parse known flags
    if err := fs.Parse(realArgs); err != nil {
        return nil, err
    }
    // The non-flag arguments from fs.Parse are in fs.Args()
    remaining := fs.Args()

    // 4) Detect all `-i <input>` patterns in `remaining`:
    var inputIndexes []int // these are indexes within `realArgs` (the original array)
    i := 0
    for i < len(remaining) {
        if remaining[i] == "-i" && (i+1 < len(remaining)) {
            // We found an input
            // But we must map back to the original indexes in realArgs
            // We'll find the real index by searching in realArgs for this substring
            // However, an easier approach is to simply find them in `remaining` and
            // store that offset. We only need to ensure we pass them to a higher-level
            // function that references realArgs. For clarity, we'll do a manual approach:
            inputFileIndex := findArgIndexInOriginal(realArgs, remaining[i+1])
            if inputFileIndex < 0 {
                return nil, fmt.Errorf("Could not find input file %q in original args", remaining[i+1])
            }
            inputIndexes = append(inputIndexes, inputFileIndex)
            i += 2
        } else {
            // skip
            i++
        }
    }

    // 5) Determine the output index.
    //    By convention, we treat the *very last argument* as the output file,
    //    unless the user used -o <file>.
    outputIndex := -1

    if *outputPtr != "" {
        // The user explicitly used -o
        // find that argument in the original array
        found := false
        for idx, arg := range realArgs {
            if arg == *outputPtr {
                // We found the output path
                // But remember realArgs might be offset from the original "args" if we stripped the 0th
                // If you want indexes in the final combined array, that’s up to you. We'll just store
                // the index in realArgs for the output.
                outputIndex = idx + (len(args) - len(realArgs)) // map back if you removed the 0th earlier
                found = true
                break
            }
        }
        if !found {
            return nil, fmt.Errorf("Output file specified by -o not found in args: %s", *outputPtr)
        }
    } else {
        // The last non-flag argument is the output
        // That means the last element of realArgs is the output
        if len(realArgs) == 0 {
            return nil, fmt.Errorf("No arguments provided at all")
        }
        outputIndex = len(args) - 1 // last item in the *full* array
    }

    // 6) Validate
    if len(inputIndexes) == 0 {
        return nil, fmt.Errorf("No input files detected (missing -i <file>)")
    }
    if outputIndex < 0 {
        return nil, fmt.Errorf("No output file determined")
    }

    return &FFmpegArgs{
        Cwd:    cwd,
        Inputs: inputIndexes,
        Output: outputIndex,
        Args:   args, // or realArgs, depending on how you want them stored
    }, nil
}

// findArgIndexInOriginal is a helper to find the index of "value" in "originalArgs".
// If you removed the 0th "ffmpeg" name, you might skip that or adapt accordingly.
func findArgIndexInOriginal(original []string, value string) int {
    for idx, v := range original {
        if v == value {
            return idx
        }
    }
    return -1
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
				var outInfoObj map[string]interface{}
				var wg sync.WaitGroup
				wg.Add(len(ffargs.Inputs) + 1)
				go func() {
					// Log sender
					defer wg.Done()

					for {
						typ, logLineObj, err := parseFFmpegTextMessage(wsConn.ReadMessage())
						if err != nil {
							logError(FFMPEG_PREFIX, "Reading logline, Websocket read error:", err)
							return
						}
						if typ == "logLine" {
							pipeTask.LogLineCh <-logLineObj["logLine"].(string)
						} else if typ == "outInfo" {
							// logLine is now over
							close(pipeTask.LogLineCh)
							outInfoObj = logLineObj
							return
						} else {
							logError(FFMPEG_PREFIX, "Reading logLine, unexpected message", logLineObj)
							return
						}
					}
				}()
				if err = processFFmpegInputs(wsConn, ffargs, &wg); err != nil {
					logError(FFMPEG_PREFIX, "Failed to write input files to websocket:", err)
					return
				}
				wg.Wait()

				// Read output metadata
				outInfoIface, ok := outInfoObj["outInfo"].([]interface{})
				if !ok {
					logError(FFMPEG_PREFIX, "Wrong output metadata object", outInfoObj, "ffargs:", ffargs)
					return
				}
				outInfo := make([]int64, 2)
				for i, iface := range outInfoIface {
					f, ok := iface.(float64)
					if !ok {
						logError(FFMPEG_PREFIX, "Wrong output metadata object", outInfoObj, "ffargs:", ffargs)
						return
					}
					outInfo[i] = int64(f)
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

				// Copy all data from the WebSocket connection to the file
				msgType, wsRd, err := wsConn.NextReader()
				if err != nil {
					logError(FFMPEG_PREFIX, "Failed to make websocket reader:", err)
					out.Close()
					return
				}
				if msgType != websocket.BinaryMessage {
					logError(FFMPEG_PREFIX, "Malformed data type from websocket:", msgType)
					out.Close()
					return
				}
				n, err := io.Copy(out, wsRd)
				if err != nil {
					logError(FFMPEG_PREFIX, "Failed to read and write to output:", outPath, "err:", err)
					out.Close()
					return
				}
				if n != outInfo[1] {
					logError(FFMPEG_PREFIX, "Size mismatch for output file", n, outInfo[1])
					out.Close()
					return
				}
				// TODO checksum
				logDebug(FFMPEG_PREFIX, "Successfully", formatBytes(n), "written as output:", outPath)
				out.Close()
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

func processFFmpegInputs(wsConn *websocket.Conn, ffargs FFmpegArgs, wg *sync.WaitGroup) error {

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

		wg.Done()

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
