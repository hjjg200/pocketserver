package main

import (
	"hash"
	"crypto/tls"
	"fmt"
	"bytes"
	"hash/crc32"
	"encoding/json"
	"io"
	"log"
	"bufio"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"mime"
	"context"
	"time"
	"strings"
)

import "embed"

//go:embed static/*
var gEmbedStatic embed.FS
var gEmbedStaticEtags map[string]string

// General app context
const METADATA_DIR = "./pocketserver-metadata"
const UPLOADS = "./uploads"
const CERT_PEM = "cert.pem"
const KEY_PEM = "key.pem"
const ROOT_CERT_PEM = "root_cert.pem"
const ROOT_KEY_PEM = "root_key.pem"
const AUTH_JSON = "auth.json"

const CONTEXT_KEY_REQUEST_ID = 0
const CONTEXT_KEY_REQUEST_START = 1
const QUERY_ALBUM = "album"
const QUERY_METADATA = "metadata"
const QUERY_CACHE = "cache"

const MIME_IMAGE = "image"
const MIME_AUDIO = "audio"
const MIME_VIDEO = "video"

const META_EXT_TXT = ".json"
const META_EXT_THUMB = ".jpg"
const META_EXT_THUMB_SMALL = "_small.webp"
const META_SLASH_IN_FILENAME = "###"
const FFMPEG_CMD_BASE = "ffmpeg -y -i '%s' "
const FFMPEG_CMD_AUDIO_THUMB = "-an -c:v copy '%s'"
const FFMPEG_CMD_AUDIO_THUMB_SMALL = "-vf 'scale=iw*sqrt(16384/(iw*ih)):-1' -an -c:v libwebp -q:v 80 '%s'"
const FFMPEG_CMD_VIDEO_THUMB = "-ss 00:00:01 -vframes 1 '%s'"
const FFMPEG_WS_SOCKET_CLOSED = "POCKETSERVER_FFMPEG_WEBSOCKET_CLOSED"
const FFMPEG_WS_SERVER_FAILED = "POCKETSERVER_FFMPEG_WEBSOCKET_SERVER_FAILED"

const LOG_RED		= "\033[31m"
const LOG_GREEN		= "\033[32m"
const LOG_BLUE		= "\033[34m"
const LOG_INVERSE	= "\033[7m"
const LOG_RESET		= "\033[0m" // Reset to default color

var gMetadataManager *MetadataManager



func uploadHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		logHTTPRequest(r, -1, "Invalid method for upload")
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	mr, err := r.MultipartReader()
	if err != nil {
		logHTTPRequest(r, -1, "r.MultipartReader err:", err)
		http.Error(w, "Cannot create multipart reader", http.StatusBadRequest)
		return
	}

	// Handling uploads
	album := filepath.Base(r.URL.Query().Get(QUERY_ALBUM))
	uploadDir := filepath.Join(gAppInfo.UploadDir, album)
	metaDir := filepath.Join(gAppInfo.MetadataDir, uploadDir)
	fullpathFile := ""
	fullpathProgress := ""
	extInProgress := "." + fmt.Sprint(time.Now().Unix()) + ".inprogress"

	keyMap := make(map[string] struct{})
	var base, crc32_0, crc32_1 string
	strPtrMap := map[string] *string{
		"crc": &crc32_0,
	}

	for {
		
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}

		// ---
		if err != nil {
			logHTTPRequest(r, -1, "mr.NextPart err:", err)
			http.Error(w, "Error reading part", http.StatusBadRequest)
			return
		}
		defer part.Close()

		// Check part key
		key := part.FormName()
		_, ok := keyMap[key]
		if ok {
			logHTTPRequest(r, -1, "Duplicate keys")
			http.Error(w, "Duplicate keys", http.StatusBadRequest)
			return
		}
		keyMap[key] = struct{}{}

		// Check part
		if strings.HasPrefix(key, "metadata:") || key == "file" {

			if len(strPtrMap) != 0 {
				logHTTPRequest(r, -1, "Keys not found:", strPtrMap)
				http.Error(w, "Keys not found", http.StatusBadRequest)
				return
			}

			if key == "file" {
				base = part.FileName()
				base = recursiveNewName(uploadDir, base)
			}

			// Upload files
			var hasher hash.Hash32
			mw := io.MultiWriter()
			fullpath := ""

			if strings.HasPrefix(key, "metadata:") {

				ext := key[9:]
				if len(ext) <= 1 {
					logHTTPRequest(r, -1, "Wrong formdata for upload metadata key:", key)
					http.Error(w, "Wrong formdata for upload", http.StatusBadRequest)
					return
				}
				fullpath = filepath.Join(metaDir, base) + ext

			} else if key == "file" {
				
				fullpathFile = filepath.Join(uploadDir, base)

				fullpath = fullpathFile + extInProgress
				fullpathProgress = fullpath
				hasher = crc32.NewIEEE()
				mw = io.MultiWriter(hasher)

			}

			// ---
			out, err := os.Create(fullpath)
			if err != nil {
				logHTTPRequest(r, -1, fullpath, "os.Create err:", err)
				http.Error(w, "Error creating file", http.StatusInternalServerError)
				return
			}
			mw = io.MultiWriter(mw, out)

			// Read the file in chunks
			buffer := make([]byte, 8*1024*1024) // 8 MB chunks // TODO perf config
			for {

				bytesRead, err := part.Read(buffer)
				if err != nil && err != io.EOF {
					logHTTPRequest(r, -1, fullpath, "part.Read err:", err)
					http.Error(w, "Error reading from part", http.StatusInternalServerError)
					out.Close()
					return
				}

				if bytesRead == 0 {
					break
				}

				// Update the hasher with the chunk
				_, err = mw.Write(buffer[:bytesRead])
				if err != nil {
					logHTTPRequest(r, -1, fullpath, "out.Write err:", err)
					http.Error(w, "Error writing to server", http.StatusInternalServerError)
					out.Close()
					return
				}

			}

			out.Close()
			// ---
			if key == "file" {
				crc32_1 = fmt.Sprintf("%08x", hasher.Sum32())
			}

			logHTTPRequest(r, -1, base, key)

		} else {
			
			ptr, ok := strPtrMap[key]
			if !ok {
				logHTTPRequest(r, -1, "Wrong form key:", key)
				http.Error(w, "Wrong form keys", http.StatusBadRequest)
				return
			}

			buf := make([]byte, 512)
			read, err := part.Read(buf)
			if err != nil && err != io.EOF {
				logHTTPRequest(r, -1, "ioutil.ReadAll err:", err)
				http.Error(w, "Error reading part", http.StatusBadRequest)
				return
			}

			*ptr = string(buf[:read])
			delete(strPtrMap, key)

		}

	}

	// ---
	if _, hasFile := keyMap["file"]; !hasFile {
		logHTTPRequest(r, -1, "file not found")
		http.Error(w, "file not found", http.StatusBadRequest)
		return
	}

	// Check file
	if crc32_0 != crc32_1 {
		logHTTPRequest(r, -1, base, "crc mismatch", crc32_0, crc32_1)
		http.Error(w, "crc doesn't match", http.StatusInternalServerError)
		return
	}

	err = os.Rename(fullpathProgress, fullpathFile)
	if err != nil {
		logHTTPRequest(r, -1, fullpathProgress, "os.Rename err:", err)
		http.Error(w, "Error changing name", http.StatusInternalServerError)
		return
	}

	info, err := ioStat(fullpathFile)
	if err != nil {
		logHTTPRequest(r, -1, fullpathFile, "ioStat err:", err)
		http.Error(w, "Error doing stat of uploaded file", http.StatusInternalServerError)
		return
	}

	// Set metadata
	err = gMetadataManager.SetMetadata(uploadDir, base, info, crc32_0)
	if err != nil {
		logHTTPRequest(r, -1, "Failed to set metadata err:", err)
		http.Error(w, "Failed to set metadata", http.StatusInternalServerError)
		return
	}

	logHTTPRequest(r, -1, "UPLOAD", base, crc32_0)

}



func editPlaylistHandler(w http.ResponseWriter, r *http.Request) {
	
	//
	dir := filepath.Join(
		gAppInfo.UploadDir,
		filepath.Base(r.URL.Query().Get(QUERY_ALBUM)),
	)

	if r.Method != http.MethodPost {
		logHTTPRequest(r, -1, "Invalid method")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Read and parse the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logHTTPRequest(r, -1, "Failed to read body", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var pl1 = make([]string, 0)
	if err := json.Unmarshal(body, &pl1); err != nil {
		logHTTPRequest(r, -1, "Failed to parse json", err)
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	err = gMetadataManager.EditPlaylist(dir, pl1)
	if err != nil {
		logHTTPRequest(r, -1, "Failed to edit playlist", err)
		http.Error(w, "Failed to edit playlist", http.StatusBadRequest)
		return
	}

	w.WriteHeader(200)

}

func listHandler(w http.ResponseWriter, r *http.Request) {

	//
	dir := filepath.Join(
		gAppInfo.UploadDir,
		filepath.Base(r.URL.Query().Get(QUERY_ALBUM)),
	)

	//
	cached := r.URL.Query().Has(QUERY_CACHE)

	if cached == false {
			
		// Update
		err := gMetadataManager.UpdateDir(dir)
		if err != nil {
			logHTTPRequest(r, -1, "Invalid directory: ", dir)
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
	
	}

	// Get cache
	data, ok := gMetadataManager.Get(dir)
	if !ok {
		logHTTPRequest(r, -1, "Invalid directory: ", dir)
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	/*if checkNotModified(r, mod) {
		w.WriteHeader(http.StatusNotModified)
		return
	}*/

	w.Header().Set("Cache-Control", "public, no-store")
	w.Header().Set("Content-Type", "application/json")
	//w.Header().Set("Last-Modified", mod.UTC().Format(http.TimeFormat)) // TODO put if modified in fetch
	fmt.Fprint(w, string(data))

}

func checkNotModified(r *http.Request, mod time.Time) bool {

	mod = mod.Truncate(time.Second)

	// Check mod time
	ifModifiedSince := r.Header.Get("If-Modified-Since")
	if ifModifiedSince != "" {

		parsedTime, err := http.ParseTime(ifModifiedSince)
		if err != nil {
			return false
		}

		if mod.Equal(parsedTime) {
			return true
		}

	}

	return false

}

func viewHandler(w http.ResponseWriter, r *http.Request) {

	// Specify the path to your file
	base		:= filepath.Base(r.URL.Path)
	query		:= r.URL.Query()
	album		:= query.Get(QUERY_ALBUM)
	dir			:= filepath.Join(gAppInfo.UploadDir, filepath.Base(album))
	fullpath	:= filepath.Join(dir, base)
	metaSuffix	:= query.Get(QUERY_METADATA)

	if metaSuffix != "" {

		metaFullpath := filepath.Join(gAppInfo.MetadataDir, fullpath) + metaSuffix

		// Check mod time
		info, err := ioStat(metaFullpath)
		if err != nil {
			// TODO if client has image advise it to use it
			logHTTPRequest(r, -1, "Failed to stat metadata err:", err)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if checkNotModified(r, info.ModTime()) {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		// Read metadata
		meta, err := ioReadFile(metaFullpath)
		if err != nil || len(meta) == 0 {
			logHTTPRequest(r, -1, "Failed to read metadata err:", err)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeContent(w, r, base+metaSuffix, info.ModTime(), bytes.NewReader(meta))

	} else {
			
		// Open the file
		file, err := ioOpen(fullpath)
		if os.IsNotExist(err) {
			logHTTPRequest(r, -1, "view Not Found", fullpath)
			w.WriteHeader(http.StatusNotFound)
			return
		} else if err != nil {
			logHTTPRequest(r, -1, "view ioOpen", fullpath,  err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		// Get file info to retrieve the modification time
		info, err := file.Stat()
		if err != nil {
			logHTTPRequest(r, -1, "view file.Stat", fullpath, err)
			http.Error(w, "Error retrieving file info", http.StatusInternalServerError)
			return
		}

		// Check mod time
		if checkNotModified(r, info.ModTime()) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		
		// Serve the content
		w.Header().Set("Cache-Control", "public, no-store")
		http.ServeContent(w, r, filepath.Base(fullpath), info.ModTime(), file)
		return
	
	}

}


func topLevelMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add required headers for cross-origin isolation
		w.Header().Set("Cross-Origin-Embedder-Policy", "require-corp")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}




type responseWriter struct {
	http.ResponseWriter
	r		*http.Request
	code	int
	written int64
	brief	string // Head of written message for error logging
}

func (w *responseWriter) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.WriteHeader(200)
	}
	n, err := w.ResponseWriter.Write(p)
	w.written += int64(n)

	// Store the first part of message for logging http.Error
	left := 256 - len(w.brief)
	if left > 0 {
		if len(p) < left {
			left = len(p)
		}
		w.brief += string(p[:left])
	}

	return n, err
}

func (w *responseWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

// Ensure responseWriter implements http.Hijacker if the underlying ResponseWriter supports it
func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	// Check if the underlying ResponseWriter supports the http.Hijacker interface
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not support http.Hijacker")
	}
	return hijacker.Hijack()
}

func performanceMiddlewareFactory(config PerformanceConfig) func(http.Handler) http.Handler {

	performance := struct{
		MemAlloc                   atomic.Uint64
		PeakMemAlloc               atomic.Uint64
		ConcurrentRequests         atomic.Int64
		PeakConcurrentRequests     atomic.Int64
		PeakNanosecondsPerRequest  atomic.Int64
		Timeouts                   atomic.Int64
		RequestCount               atomic.Int64
	}{}

	performanceSnapshot := func() map[string]interface{} {
		return map[string]interface{} {
			"memAlloc": performance.MemAlloc.Load(),
			"peakMemAlloc": performance.PeakMemAlloc.Load(),
			"concurrentRequests": performance.ConcurrentRequests.Load(),
			"peakConcurrentRequests": performance.PeakConcurrentRequests.Load(),
			"peakNanosecondsPerRequest": performance.PeakNanosecondsPerRequest.Load(),
			"timeouts": performance.Timeouts.Load(),
			"requestCount": performance.RequestCount.Load(),
		}
	}

	checkMemstats := throttle(func() {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)

		alloc := memStats.Alloc
		performance.MemAlloc.Store(alloc)
		if alloc > performance.PeakMemAlloc.Load() {
			performance.PeakMemAlloc.Store(alloc)
		}
	}, 1 * time.Second)

	// Memory Stat
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		for range ticker.C {
			checkMemstats()
		}
	}()

	atomicComparePeak := func(peak *atomic.Int64, current int64) {
		for {
			currentPeak := peak.Load()
			if current > currentPeak {
				if peak.CompareAndSwap(currentPeak, current) {
					return // Successfully updated the peak value
				}
			} else {
				return // No update needed
			}
		}
	}

	//
	var mu sync.Mutex
	type RequestInfo struct {
		URI			string		`json:"uri"`
		RemoteAddr	string		`json:"remoteAddr"`
		Since		time.Time	`json:"since"`
		Elapsed		string		`json:"elapsed"`
	}
	requestMap := make(map[string]*RequestInfo)

	apiPerformance = func(w http.ResponseWriter, r *http.Request) {

		now := time.Now()

		mu.Lock()
		for _, info := range requestMap {
			info.Elapsed = fmt.Sprint(now.Sub(info.Since))
		}
		b, err := json.MarshalIndent(map[string]interface{}{
			"stats": performanceSnapshot(),
			"requestInfos": requestMap,
		}, "", "  ")
		mu.Unlock()

		if err != nil {
			logHTTPRequest(r, -1, "failed to marshal json:", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(b)

	}

	//
	logDebug(config.MaxConcurrentRequests, "max requests")
	sem := NewSemaphore(config.MaxConcurrentRequests, config.RequestTimeout)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w0 http.ResponseWriter, r *http.Request) {
			// Record the start time
			start := time.Now()

			// Add the request ID to the context
			rID 	:= fmt.Sprintf("%d", performance.RequestCount.Add(1))
			ctx 	:= context.WithValue(r.Context(), CONTEXT_KEY_REQUEST_ID, rID)
			ctx		 = context.WithValue(ctx, CONTEXT_KEY_REQUEST_START, start)
			r 		 = r.WithContext(ctx)
			w 		:= &responseWriter{ResponseWriter: w0, r: r}
			mu.Lock()
			requestMap[rID] = &RequestInfo{
				URI: r.URL.RequestURI(),
				RemoteAddr: r.RemoteAddr,
				Since: start,
			}
			mu.Unlock()
			defer func() {
				mu.Lock()
				delete(requestMap, rID)
				mu.Unlock()
			}()

			// Semaphore for limited environment
			ok := sem.Acquire()
			if !ok {
				logHTTPRequest(r, -1, "TIMEOUT")
				http.Error(w, "Timeout", http.StatusServiceUnavailable)
				return
			}
			defer sem.Release()

			checkMemstats()
			
			// TODO add header setter to context so that later set the headers later down in mux
			//       preventing users without permission getting access to the performance info
			w.Header().Set("X-Performance", mustJsonMarshal(performanceSnapshot()))

			// Check and increment the current request count
			concurrent := performance.ConcurrentRequests.Add(1)
			defer performance.ConcurrentRequests.Add(-1)

			atomicComparePeak(&performance.PeakConcurrentRequests, concurrent)

			// Pass the request to the next handler
			next.ServeHTTP(w, r)
			if w.code == 0 {
				w.WriteHeader(200)
			} else if w.code >= 400 {
				logHTTPRequest(r, w.code, w.brief)
			} else {
				logHTTPRequest(r, w.code)
			}

			// Measure the time spent
			elapsed := time.Since(start).Nanoseconds()
			atomicComparePeak(&performance.PeakNanosecondsPerRequest, elapsed)

		})
	}

}

// https://cs.opensource.google/go/go/+/master:src/mime/type_unix.go?q=symbol%3A%5Cbmime.loadMimeFile%5Cb%20case%3Ayes
func addDefaultMimeTypes() {

	data, err := gEmbedStatic.ReadFile("static/mime.types")
	must(err)

	// Convert the []byte data to a string and split into lines
	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		// Trim whitespace and skip empty lines or comments
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}

		// Split the line into fields
		fields := strings.Fields(line)
		if len(fields) <= 1 {
			continue
		}

		// The first field is the MIME type
		mimeType := fields[0]

		// The remaining fields are the extensions
		for _, ext := range fields[1:] {
			// Skip any fields that start with '#' (comments within a line)
			if ext[0] == '#' {
				break
			}
			// Add the extension and MIME type to the map
			mime.AddExtensionType("."+ext, mimeType)
		}
	}
}


func main() {

	now := time.Now()
	gAppInfo.Start = now

	// iSH compatibility
	addDefaultMimeTypes()

	// Check if it is invoked as ffmpeg and if so run as ffmpeg
	checkRunAsFFmpeg()

	// Flags
	parseFlag()

	// Path
	must(changeToExecDir())

	// Embed
	populateEmbedEtags()

	// Dirs
	home, err := os.UserHomeDir()
	must(err)
	gAppInfo.UploadDir = filepath.Join(".", UPLOADS)
	gAppInfo.MetadataDir = filepath.Join(home, METADATA_DIR) // use emulated disk for better performance
	logInfo("Upload directory is", gAppInfo.UploadDir)
	logInfo("Metadata directory is", gAppInfo.MetadataDir)
	logWarn("Ensure metadata directory is not in mounted icloud drive for performance issue")

	// Upload Directory and cache
	must(os.MkdirAll(gAppInfo.UploadDir, 0755))
	must(os.MkdirAll(gAppInfo.MetadataDir, 0755))

	// Branch out to test if test is specified
	if (gAppInfo.Test != "") {
		runTests()
		os.Exit(0)
	}

	// Start metadata manager
	gMetadataManager = NewMetadataManager()
	must(gMetadataManager.LoadDirCaches())
	go func() {
		dentries, err := ioReadDir(gAppInfo.UploadDir)
		must(err)
		dirs := []string{}
		dirs = append(dirs, gAppInfo.UploadDir)
		for _, dentry := range dentries {
			if dentry.IsDir() {
				dirs = append(dirs, filepath.Join(gAppInfo.UploadDir, dentry.Name()))
			}
		}
		for _, dir := range dirs {
			gMetadataManager.AddDir(dir)
			if err = gMetadataManager.UpdateDir(dir); err != nil {
				logFatal(fmt.Errorf("Failed to cache dir %s: %w", dir, err))
			}
		}
	}()

	// IP
	gAppInfo.LocalIPs = resolveLocalIPs()

	// MUX
	mux := http.NewServeMux()
	mux.HandleFunc("/", staticHandler)
	mux.HandleFunc("/static/", staticHandler)
	mux.HandleFunc("/ping", pingHandler)
	mux.HandleFunc("/view/", viewHandler)
	mux.HandleFunc("/upload", uploadHandler)
	mux.HandleFunc("/list", listHandler)
	mux.HandleFunc("/editPlaylist", editPlaylistHandler)
	mux.HandleFunc("/signout", signoutHandler)
	mux.Handle("/api/", apiMux)

	// MUX - handlers that need initialization
	// init ffmpeg http handler and the relevant unix socket
	initFFmpegSocket()
	mux.HandleFunc("/ws/ffmpeg", ffmpegHandler)

	//
	performanceMiddleware := performanceMiddlewareFactory(gPerformanceConfig)

	// HTTPS :443 mux
	httpsMux := authMiddleware(mux)
	httpsMux  = topLevelMiddleware(httpsMux)
	httpsMux  = performanceMiddleware(httpsMux)

	// http :80 mux
	httpMux := httpFilterMiddleware(mux)
	httpMux  = topLevelMiddleware(httpMux)
	httpMux  = performanceMiddleware(httpMux)

	// Auth
	loadAuthCookies()

	ensureTLSCertificate("cert.pem", "key.pem", gAppInfo.LocalIPs)
	server := &http.Server{
		Addr: ":443",
		Handler: httpsMux,
		TLSConfig: &tls.Config{
			//Certificates: []tls.Certificate{cert},
		},
		ErrorLog: log.New(io.Discard, "", 0), // Discard https errors which pop up so frequently due to untrusted certs
	}

	// HTTP server
	go func() {
		for {
			logError(http.ListenAndServe(":80", httpMux))
			time.Sleep(time.Second * 10)
			logError("Attempting HTTP restart...")
		}
	}()

	// Start the server
	logInfo("SERVER STARTED AT", now.Format(time.RFC3339), fmt.Sprint("(", time.Now().Sub(now),")"))
	logInfo()
	logInfo("http://127.0.0.1   (recommended)")
	for _, ipStr := range gAppInfo.LocalIPs {
		if ipStr == "127.0.0.1" || ipStr == "::1" {
			continue
		}
		if isIPv4(ipStr) {
			logInfo("https://" + ipStr)
		} else {
			logInfo("https://[" + ipStr + "]")
		}
	}

	for {
		logError(server.ListenAndServeTLS(CERT_PEM, KEY_PEM)) // Certificates are already provided in memory
		time.Sleep(time.Second * 10)
		logError("Attempting HTTPS restart...")
	}

}
