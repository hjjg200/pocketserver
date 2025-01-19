package main

import (
	"bufio"
	"fmt"
	"flag"
	"net"
	"os"
	"encoding/json"
	"path/filepath"
	"net/http"
	"strings"

    "github.com/gorilla/websocket"
)

const FFMPEG_PREFIX = "[FFmpeg]"


func initFFmpeg() {
	// Check if the program is invoked as "ffmpeg" or the main app
	if filepath.Base(os.Args[0]) == "ffmpeg2" && len(os.Args) > 1 {
		subFFmpeg(os.Args)
		logInfo(FFMPEG_PREFIX, "Packet sent to main worker")
		os.Exit(0)
	} else {
		ffmpegHandler = makeFFmpegHandler()
	}
}




type FFmpegArgs struct {
	Inputs	[]int		`json:"inputs"`
	Output	int			`json:"output"`
	Args 	[]string	`json:"args"`
}

// parseFFmpegArgs parses ffmpeg-style arguments and returns indexes for inputs and output.
func parseFFmpegArgs(args []string) (*FFmpegArgs, error) {
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
    ch := make(chan string)

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

					logDebug(FFMPEG_PREFIX, "Sending to channel")
					ch <-ffargsJson
			
					// Simulate processing the arguments
					response := fmt.Sprintf("Processed command: %s", ffargsJson)
					logInfo(FFMPEG_PREFIX, "Sending response:", response)
			
					// Write response back to the subordinate
					fmt.Fprintln(conn, response)
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
            // msgType will be websocket.TextMessage or Binary, etc.
            msgType, msg, err := wsConn.ReadMessage()
            if err != nil {
                logError(FFMPEG_PREFIX, "Websocket read error:", err)
                return
            }
            line := strings.TrimSpace(string(msg))
            logDebug(FFMPEG_PREFIX, "Received:", line)

            if line == "ready" {
                logDebug(FFMPEG_PREFIX, "Browser is ready")
                // Wait for an "args" string from the channel
                argsJson := <-ch

                // Send back to the browser
                err = wsConn.WriteMessage(msgType, []byte(argsJson))
                if err != nil {
                    logError(FFMPEG_PREFIX, "Write error:", err)
                    return
                }
            }
        }
    }
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
	logInfo(FFMPEG_PREFIX, "Sent arguments:", string(ffargsJson))

	// Read response from the main worker
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		response := scanner.Text()
		logInfo(FFMPEG_PREFIX, "Response from main worker:", response)
	}

	if err := scanner.Err(); err != nil {
		logInfo(FFMPEG_PREFIX, "Scanner error:", err)
	}
}
