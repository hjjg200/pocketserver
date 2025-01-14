package main

import (
	"crypto/tls"
	"fmt"
	"bytes"
	"hash/crc32"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"encoding/base64"
	"encoding/json"
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
const METADATA_DIR = "./metadata"
const UPLOADS = "./uploads"
const CERT_PEM = "cert.pem"
const KEY_PEM = "key.pem"
const ROOT_CERT_PEM = "root_cert.pem"
const ROOT_KEY_PEM = "root_key.pem"
const AUTH_JSON = "auth.json"

const CONTEXT_KEY_REQUEST_ID = 0
const QUERY_ALBUM = "album"
const QUERY_THUMBNAIL = "thumbnail"
const QUERY_DETAILS = "details"

const MIME_IMAGE = "image"
const MIME_AUDIO = "audio"
const MIME_VIDEO = "video"

const META_EXT_TXT = ".json"
const META_EXT_THUMB = ".jpg"
const META_EXT_THUMB_SMALL = "_small.webp"
const FFMPEG_CMD_BASE = "ffmpeg -y -i '%s' "
const FFMPEG_CMD_AUDIO_THUMB = "-an -c:v copy '%s'"
const FFMPEG_CMD_AUDIO_THUMB_SMALL = "-vf 'scale=iw*sqrt(16384/(iw*ih)):-1' -an -c:v libwebp -q:v 80 '%s'"
const FFMPEG_CMD_VIDEO_THUMB = "-ss 00:00:01 -vframes 1 '%s'"

const LOG_RED		= "\033[31m"
const LOG_GREEN		= "\033[32m"
const LOG_BLUE		= "\033[34m"
const LOG_INVERSE	= "\033[7m"
const LOG_RESET		= "\033[0m" // Reset to default color

var gMetadataManager *MetadataManager



func uploadHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		logHTTPRequest(r, http.StatusMethodNotAllowed, "Invalid method for upload")
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	mr, err := r.MultipartReader()
	if err != nil {
		logHTTPRequest(r, http.StatusBadRequest, "r.MultipartReader err:", err)
		http.Error(w, "Cannot create multipart reader", http.StatusBadRequest)
		return
	}

	hashHex0 := ""
	hashHex1 := ""
	fileCount := 0
	fn := ""
	fullpath := ""
	fullpathUndone := ""
	for {

		part, err := mr.NextPart()

		if err == io.EOF {
			break
		}
		if err != nil {
			logHTTPRequest(r, http.StatusBadRequest, "mr.NextPart err:", err)
			http.Error(w, "Error reading part", http.StatusBadRequest)
			return
		}
		defer part.Close()

		n := part.FormName()
		if n == "hashHex" {
		
			p, err := ioutil.ReadAll(part)
			if err != nil {
				logHTTPRequest(r, http.StatusBadRequest, "ioutil.ReadAll err:", err)
				http.Error(w, "Error reading part", http.StatusBadRequest)
				return
			}
			hashHex0 = string(p)
		
		} else if part.FormName() == "file" {

			if fileCount > 0 {
				logHTTPRequest(r, http.StatusBadRequest, "Too many files")
				http.Error(w, "Too many files", http.StatusBadRequest)
				return
			}

			fileCount += 1

			// Path for upload
			dir := filepath.Join(
				gAppInfo.UploadDir,
				filepath.Base(r.URL.Query().Get(QUERY_ALBUM)),
			)

			// Generate a unique filename
			fn = part.FileName()
			fn = filepath.Base(fn)
			fn = recursiveNewName(dir, fn)

			fullpath = filepath.Join(dir, fn)
			fullpathUndone = filepath.Join(dir, fn + fmt.Sprint(time.Now().Unix()) + ".inprogress")

			// Create the file and hasher
			hasher := crc32.NewIEEE()
			out, err := os.Create(fullpathUndone)
			if err != nil {
				logHTTPRequest(r, http.StatusInternalServerError, fullpathUndone, "os.Create err:", err)
				http.Error(w, "Error creating file", http.StatusInternalServerError)
				return
			}

			// Read the file in chunks
			buffer := make([]byte, 8*1024*1024) // 8 MB chunks // TODO perf config
			for {

				bytesRead, err := part.Read(buffer)
				if err != nil && err != io.EOF {
					logHTTPRequest(r, http.StatusInternalServerError, fullpathUndone, "part.Read err:", err)
					http.Error(w, "Error reading from part", http.StatusInternalServerError)
					out.Close()
					return
				}

				if bytesRead == 0 {
					break
				}

				// Update the hasher with the chunk
				_, err = out.Write(buffer[:bytesRead])
				if err != nil {
					logHTTPRequest(r, http.StatusInternalServerError, fullpathUndone, "out.Write err:", err)
					http.Error(w, "Error writing to server", http.StatusInternalServerError)
					out.Close()
					return
				}

				hasher.Write(buffer[:bytesRead])

			}

			// Hash
			out.Close()
			hashHex1 = fmt.Sprint(hasher.Sum32())

		}

	}

	// Check hash
	if hashHex0 != hashHex1 {
		logHTTPRequest(r, http.StatusInternalServerError, fullpathUndone, "hashHex mismatch", hashHex0, hashHex1)
		http.Error(w, "Hash doesn't match", http.StatusInternalServerError)
		return
	}

	// Finalize
	err = os.Rename(fullpathUndone, fullpath)
	if err != nil {
		logHTTPRequest(r, http.StatusInternalServerError, fullpathUndone, "os.Rename err:", err)
		http.Error(w, "Error changing name", http.StatusInternalServerError)
		return
	}

	logHTTPRequest(r, -1, "UPLOAD", fn, hashHex1)

}


func listHandler(w http.ResponseWriter, r *http.Request) {

	//
	dir := filepath.Join(
		gAppInfo.UploadDir,
		filepath.Base(r.URL.Query().Get(QUERY_ALBUM)),
	)

	// Update
	err := gMetadataManager.UpdateDir(dir)
	logDebug("update dir", dir)
	if err != nil {
		logHTTPRequest(r, http.StatusBadRequest, "Invalid directory: ", dir)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Get cache
	data, mod, ok := gMetadataManager.Get(dir, r.URL.Query().Has(QUERY_DETAILS))
	if !ok {
		logHTTPRequest(r, http.StatusNotFound, "Invalid directory: ", dir)
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	// TODO check if modified
	if checkNotModified(r, mod) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	logDebug("HEADERS", r.Header, "mod:", mod)

	w.Header().Set("Cache-Control", "public, no-cache")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Last-Modified", mod.UTC().Format(http.TimeFormat))
	fmt.Fprint(w, string(data))

}

func checkNotModified(r *http.Request, mod time.Time) bool {

	mod = mod.Truncate(time.Second)

	// Check mod time
	ifModifiedSince := r.Header.Get("If-Modified-Since")
	if ifModifiedSince != "" {

		parsedTime, err := http.ParseTime(ifModifiedSince)
		if err != nil {
			logDebug("Parse fail", parsedTime, "err:", err)
			return false
		}

		logDebug(mod.Sub(parsedTime), "MODTIME", mod, parsedTime)
		if mod.Equal(parsedTime) {
			return true
		}

	}

	return false

}

func checkPreservedAlbum(w http.ResponseWriter, r *http.Request, album string) bool {

	w.Header().Set("X-Preserved-Albums", base64.StdEncoding.EncodeToString([]byte("[]")))

	//
	cookie, err := r.Cookie("preservedAlbums")

	if err != nil {
		return false
	}

	decodedBytes, err := base64.StdEncoding.DecodeString(cookie.Value)
	if err != nil {
		logWarn("Failed to read preserved albums", err)
		return false
	}

	var albums = make([]string, 0)
	err = json.Unmarshal(decodedBytes, &albums)
	if err != nil {
		logWarn("Failed to read preserved albums json", err)
		return false
	}
	
	logDebug("PRESERVED", albums)
	w.Header().Set("X-Preserved-Albums", cookie.Value)

	for _, rhs := range albums {
		if rhs == album {
			return true
		}
	}
	return false

}

func viewHandler(w http.ResponseWriter, r *http.Request) {

	// Specify the path to your file
	base	 := filepath.Base(r.URL.Path)
	query    := r.URL.Query()
	album	 := query.Get(QUERY_ALBUM)
	dir 	 := filepath.Join(
		gAppInfo.UploadDir,
		filepath.Base(album),
	)
	fullpath := filepath.Join(dir, base)
	preserved := checkPreservedAlbum(w, r, album)

	if query.Has(QUERY_THUMBNAIL) {

		// Paths
		thumbpath := filepath.Join(gAppInfo.MetadataDir, fullpath)
		if query.Get(QUERY_THUMBNAIL) == "small" {
			thumbpath += META_EXT_THUMB_SMALL
			w.Header().Set("Content-Type", "image/webp")
		} else {
			thumbpath += META_EXT_THUMB
			w.Header().Set("Content-Type", "image/jpeg")
		}

		// Check mod time
		info, err := os.Stat(thumbpath)
		if err != nil {
			// TODO if client has image advise it to use it
			logHTTPRequest(r, -1, "Failed to read thumbnail err:", err)
			http.Redirect(w, r, "/static/default_artwork.jpg", http.StatusSeeOther)
			return
		}
		if preserved && checkNotModified(r, info.ModTime()) {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		// Read thumbnail
		thumb, err := os.ReadFile(thumbpath)
		if err != nil || len(thumb) == 0 {
			logHTTPRequest(r, -1, "Failed to read thumbnail err:", err)
			http.Redirect(w, r, "/static/default_artwork.jpg", http.StatusSeeOther)
			return
		}

		if preserved {
			w.Header().Set("Cache-Control", "public, no-cache")
		}
		http.ServeContent(w, r, base, info.ModTime(), bytes.NewReader(thumb))

	} else {
			
		// Open the file
		file, err := os.Open(fullpath)
		if os.IsNotExist(err) {
			logHTTPRequest(r, http.StatusNotFound, "view Not Found", fullpath)
			http.Error(w, "File not found", http.StatusNotFound)
			return
		} else if err != nil {
			logHTTPRequest(r, http.StatusInternalServerError, "view os.Open", fullpath,  err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		// Get file info to retrieve the modification time
		info, err := file.Stat()
		if err != nil {
			logHTTPRequest(r, http.StatusInternalServerError, "view file.Stat", fullpath, err)
			http.Error(w, "Error retrieving file info", http.StatusInternalServerError)
			return
		}

		// Check mod time
		if preserved && checkNotModified(r, info.ModTime()) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		
		// Serve the content
		if preserved {
			w.Header().Set("Cache-Control", "public, no-cache")
		}
		http.ServeContent(w, r, filepath.Base(fullpath), info.ModTime(), file)
		return
	
	}

}






type responseWriter struct {
	http.ResponseWriter
	r *http.Request
	code int
}

func (w *responseWriter) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}

func (w *responseWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
	logHTTPRequest(w.r, code)
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

	checkMemstats := throttleAtomic(func() {
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
	sem := NewSemaphore(config.MaxConcurrentRequests, config.RequestTimeout)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w0 http.ResponseWriter, r *http.Request) {

			// Record the start time
			start := time.Now()

			// Add the request ID to the context
			rID 	:= fmt.Sprintf("%d", performance.RequestCount.Add(1))
			ctx 	:= context.WithValue(r.Context(), CONTEXT_KEY_REQUEST_ID, rID)
			r 		 = r.WithContext(ctx)
			w 		:= &responseWriter{ResponseWriter: w0, r: r}
			
			//logHTTPRequest(r, -1, "new request")

			// Semaphore for limited environment
			ok := sem.Acquire()
			if !ok {
				logHTTPRequest(r, http.StatusServiceUnavailable, "TIMEOUT")
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

	fmt.Println()
	now := time.Now()
	gAppInfo.Start = now

	parseFlag()

	// Path
	must(changeToExecDir())

	// Embed
	populateEmbedEtags()
	addDefaultMimeTypes()

	// Dirs
	gAppInfo.UploadDir = mustFilepathRel(".", UPLOADS)
	gAppInfo.MetadataDir = mustFilepathRel(".", METADATA_DIR)

	// Upload Directory and cache
	must(os.MkdirAll(gAppInfo.UploadDir, 0755))
	must(os.MkdirAll(gAppInfo.MetadataDir, 0755))

	gMetadataManager = NewMetadataManager()
	dentries, err := os.ReadDir(gAppInfo.UploadDir)
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

	// IP
	li, _ := getOutboundIPs()
	gAppInfo.LocalIP = li

	// MUX
	mux := http.NewServeMux()
	mux.HandleFunc("/", staticHandler)
	mux.HandleFunc("/static/", staticHandler)
	mux.HandleFunc("/service-worker.js", staticHandler)
	mux.HandleFunc("/ping", pingHandler)
	mux.HandleFunc("/view/", viewHandler)
	mux.HandleFunc("/upload", uploadHandler)
	mux.HandleFunc("/list", listHandler)
	mux.HandleFunc("/signout", signoutHandler)

	//
	performanceMiddleware := performanceMiddlewareFactory(gPerformanceConfig)

	// Create an HTTPS server
	httpsMux := authMiddleware(mux)
	httpsMux  = performanceMiddleware(httpsMux)

	// Auth
	loadAuthCookies()

	ensureTLSCertificate("cert.pem", "key.pem", li)
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
		httpMux := httpFilterMiddleware(mux)
		httpMux  = performanceMiddleware(httpMux)

		http.ListenAndServe(":80", httpMux)
	}()

	// Start the server
	logInfo("SERVER STARTED AT", now.Format(time.RFC3339), fmt.Sprint("(", time.Now().Sub(now),")"))
	logInfo("http://" + gAppInfo.LocalIP)
	logInfo("https://" + gAppInfo.LocalIP)
	logFatal(server.ListenAndServeTLS(CERT_PEM, KEY_PEM)) // Certificates are already provided in memory

}
