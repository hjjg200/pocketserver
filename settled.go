package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"io/ioutil"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"sync"
	"runtime"
)


type AppInfo struct {
	Start time.Time
	LocalIPs []string // preferred local ips
	UploadCount int // Total count of uploads since startup // TODO atomic
	UploadDir string
	MetadataDir string
	Debug bool
}

var gAppInfo AppInfo



func pingHandler(w http.ResponseWriter, r *http.Request) {
	if gAppInfo.Debug {

		w.Header().Set("X-Debug", "true")

		d, err := ioutil.ReadAll(r.Body)
		if err != nil {
			logDebug(err)
			return
		}
		if len(d) == 0 {
			return
		}

		var args []string
		err = json.Unmarshal(d, &args)
		if err != nil {
			logDebug(err)
			return
		}
		cl := "[console.log] "
		if r.Header.Get("X-Debug") == "error" {
			cl = LOG_RED + "[console.error] "
		}
		logHTTPRequest(r, -1, fmt.Sprintf("\n%s%s%s", cl, strings.Join(args, "\n"), LOG_RESET))

	}
	fmt.Fprint(w, "imageserverpong")
}

func serveJson(w http.ResponseWriter, r *http.Request, data interface{}) {
	
	jsonData, err := json.Marshal(data)
	if err != nil {
		logHTTPRequest(r, -1, "Error creating JSON err:", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	// Print the JSON data
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, string(jsonData))

}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func logTimestamp(logLine string, items ...interface{}) {
	timestamp := formatShortTimestamp(gAppInfo.Start, time.Now())
	strItems := make([]string, len(items))
	for i, item := range items {
		strItems[i] = fmt.Sprint(item)
	}
	fmt.Fprintln(os.Stderr, timestamp, logLine, strings.Join(strItems, " "))
}

func logInfo(items ...interface{}) {
	logTimestamp("[info]", items...)
}
func logWarn(items ...interface{}) {
	logTimestamp("["+LOG_RED+"warn"+LOG_RESET+"]", items...)
}
func logError(items ...interface{}) {
	items = append(items, LOG_RESET)
	logTimestamp(LOG_RED+"["+"ERROR"+"]", items...)
}
func logFatal(items ...interface{}) {
	items = append(items, LOG_RESET)
	logTimestamp(LOG_RED+LOG_INVERSE+"["+"FATAL"+"]", items...)
	os.Exit(1)
}
func logTime(items ...time.Time) time.Time {
	if len(items) == 0 {
		return time.Now()
	}
	logDebug(time.Since(items[0]))
	return time.Now()
}
func logDebug(items ...interface{}) {
	if gAppInfo.Debug {

		pc, f, l, _ := runtime.Caller(1)
		dir			:= filepath.Base(filepath.Dir(f))
		f			 = dir + "/" + filepath.Base(f)
		fn			:= runtime.FuncForPC(pc)
		n			:= filepath.Base(fn.Name())
		caller		:= fmt.Sprintf("%s[%s:%d]", n, f, l)

		var buf [64]byte
		var id int64
		read := runtime.Stack(buf[:], false)
		stack := buf[:read]
		idField := bytes.Fields(stack)[1] // Extract the second field: "goroutine 123 [running]"
		fmt.Sscan(string(idField), &id)

		logTimestamp(LOG_BLUE+"[debug] "+fmt.Sprint(id)+" "+caller+LOG_RESET, items...)
	}
}

func logHTTPRequest(r *http.Request, status int, items ...interface{}) {

	// Get request id from context
	rID := "???"
	if id, ok := r.Context().Value(CONTEXT_KEY_REQUEST_ID).(string); ok {
		rID = "#" + id
	}

	//
	elapsedStr := ""
	if start, ok := r.Context().Value(CONTEXT_KEY_REQUEST_START).(time.Time); ok {
		elapsed := time.Since(start)
		elapsedStr = fmt.Sprint(elapsed)
		if elapsed >= time.Millisecond * 100 { // 100ms
			elapsedStr = LOG_RED + elapsedStr + LOG_RESET
		}
	}
	
	protocol := "HTTP "
	if r.TLS != nil {
		protocol = LOG_GREEN+"HTTPS"+LOG_RESET
	}
	method  := r.Method
	url 	:= r.URL.Path
	raddr 	:= r.RemoteAddr

	// Status string
	statusStr := fmt.Sprint(status)
	if status == -1 {
		statusStr = "---"
	} else if status >= 400 {
		statusStr = LOG_RED + statusStr + LOG_RESET
	} else if status >= 300 {
		statusStr = LOG_BLUE + statusStr + LOG_RESET
	} else if status >= 200 {
		statusStr = LOG_GREEN + statusStr + LOG_RESET
	}

	tail := ""
	if len(items) > 0 {
		tail = ":"
	}

	// Log in the desired format
	logLine := fmt.Sprintf(
		"%s %s %s %s (%s) %s <-%s%s",
		rID, protocol, method, statusStr, elapsedStr, url, raddr, tail,
	)
	logTimestamp(logLine, items...)

}


func recursiveNewName(dir string, fn string) string {
	ext := filepath.Ext(fn)
	stem := filepath.Base(fn)
	stem = stem[:len(stem)-len(ext)]
	fn1 := fn

	for i := 0; i < 5; i++ {

		if i > 0 {
			fn1 = fmt.Sprintf("%s-%d%s", stem, i+1, ext)
		}
		// TODO
		if _, err := os.Stat(filepath.Join(dir, fn1)); err == nil {
			continue
		} else if os.IsNotExist(err) {
			return fn1
		} else {
			break
		}

	}
	return fmt.Sprintf("%s-%d%s", stem, time.Now().Unix(), ext)
}

func getUploadFullpath(album, base string) string {
	return filepath.Join(gAppInfo.UploadDir, album, base)
}

func getMetadataFullpath(album, base, ext string) string {
	return filepath.Join(gAppInfo.MetadataDir, gAppInfo.UploadDir, album, base+ext)
}

func makeAuthCookie(val string, exp time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     AUTH_COOKIE_NAME,
		Value:    val,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		Secure:   true,
	}
}

func loadAuthCookies() {

	data, err := os.ReadFile(AUTH_JSON)
	if err != nil {
		logInfo("No auth cookies found")
		data = []byte("{}")
		must(os.WriteFile(AUTH_JSON, data, 0600))
	}

	gAuthInfo.ExpiryMap = make(map[string] time.Time)
	must(json.Unmarshal(data, &gAuthInfo.ExpiryMap))

	//
	expired := false
	now := time.Now()
	for k, v := range gAuthInfo.ExpiryMap {
		if now.After(v) {
			delete(gAuthInfo.ExpiryMap, k)
			logWarn("Cookie", k, "expired at", v)
			expired = true
		}
	}
	if expired {
		must(storeAuthCookies())
	}

	logInfo("Loaded", len(gAuthInfo.ExpiryMap), "authenticated users")
}

func storeAuthCookies() error {
	data, err := json.MarshalIndent(gAuthInfo.ExpiryMap, "", "  ")
	if err != nil {
		return err
	}

	logInfo("Stored", len(gAuthInfo.ExpiryMap), "authenticated users")
	return os.WriteFile(AUTH_JSON, data, 0600)
}

// Authentication middleware

const BAD_TRIES_TOLERANCE	= 10
const AUTH_COOKIE_NAME		= "auth"
const AUTH_COOKIE_LIFE		= time.Hour * 24 * 3
const AUTH_COOKIE_LENGTH	= 64

type AuthInfo struct {
	SessionPassword	string
	ExpiryMap		map[string] time.Time
	ExpiryMapMu		sync.Mutex
	BadTries		int
}
var gAuthInfo AuthInfo

type HTTPInfo struct {
	Enabled				bool
	EnablerRemoteIP		string
	AddressesHash		string
}
var gHTTPInfo HTTPInfo

func authMiddleware(next http.Handler) http.Handler {

	redirectToHTTPIndex := func(w http.ResponseWriter, r *http.Request) {
		httpURL := fmt.Sprintf("http://%s/", r.Host)
		logHTTPRequest(r, -1, "to HTTP") // SeeOther is for GET only
		http.Redirect(w, r, httpURL, http.StatusSeeOther)
	}

	handleEnableHTTP := func(w http.ResponseWriter, r *http.Request) {

		//
		raddr, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			logHTTPRequest(r, -1, "net.SplitHostPort err:", err)
			http.Error(w, "Error getting address", http.StatusBadRequest)
			return
		}

		// Set HTTP info for later checkups
		gHTTPInfo.EnablerRemoteIP = raddr

		addrs := resolveLocalIPs()
		if err != nil {
			logHTTPRequest(r, -1, "handleEnableHTTP getLocalAddresses", err)
			http.Error(w, "Error getting address", http.StatusInternalServerError)
			return
		}
		gHTTPInfo.AddressesHash = generateAddressesHash(addrs)

		// Enable HTTP
		gHTTPInfo.Enabled = true

		// Redirect to the HTTP root (/) of the same host
		redirectToHTTPIndex(w, r)

	}

	// Debounce storing cookies for 10 seconds
	fileUpdater := debounce(func() {
		
		gAuthInfo.ExpiryMapMu.Lock()
		defer gAuthInfo.ExpiryMapMu.Unlock()

		err := storeAuthCookies()
		if err != nil {
			logError("Failed to store auth file", err)
			return
		}

	}, time.Second * 10)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		now := time.Now()

		// Remote addr
		raddr, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			logHTTPRequest(r, -1, "ListenAndServe net.SplitHostPort err:", err)
			http.Error(w, "Error getting address", http.StatusBadRequest)
			return
		}

		// Localhost privilege
		if raddr == "127.0.0.1" || raddr == "::1" {
			redirectToHTTPIndex(w, r)
			return
		}

		// Check for existing cookie
		cookie, err := r.Cookie(AUTH_COOKIE_NAME)

		if err == nil {
			v := cookie.Value
			if len(v) == AUTH_COOKIE_LENGTH {

				gAuthInfo.ExpiryMapMu.Lock()
				expiry, ok := gAuthInfo.ExpiryMap[v]

				if ok {

					if now.Before(expiry) {

						// Update the expiry
						updatedExpiry := now.Add(AUTH_COOKIE_LIFE)
						http.SetCookie(w, makeAuthCookie(v, updatedExpiry))
						
						gAuthInfo.ExpiryMap[v] = updatedExpiry
						gAuthInfo.ExpiryMapMu.Unlock()

						fileUpdater()

						// If cookie exists and is valid, pass request to the main handler
						//logHTTPRequest(r, -1, "VALID COOKIE")
						next.ServeHTTP(w, r)

						return

					} else {
						logHTTPRequest(r, -1, "COOKIE EXPIRED")
						delete(gAuthInfo.ExpiryMap, v)
					}

				} else {
					logHTTPRequest(r, -1, "COOKIE EXPIRED")
				}

				fileUpdater()

				gAuthInfo.ExpiryMapMu.Unlock()

			} else {
				logHTTPRequest(r, -1, "MALFORMED COOKIE")
			}
		}

		// If the method is POST, process password input
		if r.Method == http.MethodPost {

			// Parse the form data
			if err := r.ParseForm(); err != nil {
				logHTTPRequest(r, -1, "authMiddleware r.ParseForm err:", err)
				http.Error(w, "Error parsing form", http.StatusBadRequest)
				return
			}

			// Check if the password matches
			inputPassword := r.FormValue("password")
			enableHTTP := r.FormValue("enable-http")

			if inputPassword == gAuthInfo.SessionPassword {

				gAuthInfo.BadTries = 0

				//
				newValue, err := generateRandomString(AUTH_COOKIE_LENGTH)
				if err != nil {
					logHTTPRequest(r, -1, "authMiddleware generateRandomString err:", err)
					http.Error(w, "Error generating cookie value", http.StatusInternalServerError)
					return
				}

				// Set the authentication cookie
				newExpiry := now.Add(AUTH_COOKIE_LIFE)
				http.SetCookie(w, makeAuthCookie(newValue, newExpiry))
				
				gAuthInfo.ExpiryMapMu.Lock()
				gAuthInfo.ExpiryMap[newValue] = newExpiry
				gAuthInfo.ExpiryMapMu.Unlock()

				fileUpdater()

				// Check if the user wants HTTP
				if enableHTTP == "on" {
					handleEnableHTTP(w, r)
					return
				}

				logHTTPRequest(r, -1, "cookie is created")
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return

			}

			// If the password is incorrect, show an error
			gAuthInfo.BadTries += 1
			logHTTPRequest(r, -1, "Bad password attempts:", gAuthInfo.BadTries)
			http.Redirect(w, r, "/", http.StatusSeeOther)

			if gAuthInfo.BadTries >= BAD_TRIES_TOLERANCE {
				logHTTPRequest(r, http.StatusServiceUnavailable, "Too many bad password attempts")
				logFatal("FORCE SHUTDOWN")
			}

			return
		}

		// If no cookie or incorrect method, show the password input form
		showLoginForm(w, r)
	})
}


// Display a simple password input form
func showLoginForm(w http.ResponseWriter, r *http.Request) {

	logHTTPRequest(r, -1, "login form")
	fmt.Fprintln(w, `
		<!DOCTYPE html>
		<html>
		<head>
			<title>Login</title>
			<meta charset="utf-8">
			<meta name="viewport" content="width=device-width, initial-scale=1">
			<style>
html, body {
	font-size: 24px;
    height: 100vh;
}
input, button {
	font-size: 1rem;
}
.flex {
	display: flex;
	justify-content: center;
	align-items: center;
}
			</style>
		</head>
		<body class="flex">
			<form method="POST" action="/">
				<div>
					<input id="password" type="password" name="password" placeholder="Password" required />
				</div>
				<div>
					<label>
						<input type="checkbox" name="enable-http" value="on" />
						Enable HTTP
					</label>
				</div>
				</br>
				<div class="flex">
					<button type="submit">Submit</button>
				</div>
			</form>
			<script>

document.getElementById("password").focus();

			</script>
		</body>
		</html>
	`)
}

func signoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, makeAuthCookie("", time.Unix(0, 0)))

	logHTTPRequest(r, -1, "signout requested")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func httpFilterMiddleware(next http.Handler) http.Handler {

	redirectToHTTPS := func(w http.ResponseWriter, r *http.Request) {
		target := fmt.Sprintf("https://%s%s", r.Host, r.URL.RequestURI())
		logHTTPRequest(r, -1, "to HTTPS") // SeeOther is for GET only
		http.Redirect(w, r, target, http.StatusFound)
	}

	addrsHash := ""
	addrsChecker := throttle(func() {
		addrs := resolveLocalIPs()
		addrsHash = generateAddressesHash(addrs)
	}, time.Second * 30)

	return http.HandlerFunc(func (w http.ResponseWriter, r *http.Request) {

		raddr, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			logHTTPRequest(r, -1, "ListenAndServe net.SplitHostPort err:", err)
			redirectToHTTPS(w, r)
			return
		}

		// Localhost privilege
		if raddr == "127.0.0.1" || raddr == "::1" {
			next.ServeHTTP(w, r)
			return
		}

		// Temporary HTTP
		if gHTTPInfo.Enabled {
			if raddr != gHTTPInfo.EnablerRemoteIP {
				gHTTPInfo.Enabled = false
				logHTTPRequest(r, -1, "HTTP is disabled, raddr not matching", gHTTPInfo.EnablerRemoteIP, raddr)
				redirectToHTTPS(w, r)
				return
			}

			addrsChecker()
			if addrsHash == "" || addrsHash != gHTTPInfo.AddressesHash {
				gHTTPInfo.Enabled = false
				logHTTPRequest(r, -1, "HTTP is disabled, interface hash mismatch", gHTTPInfo.AddressesHash, addrsHash)
				redirectToHTTPS(w, r)
				return
			}
				
			// Handle temporaryily disabled https
			next.ServeHTTP(w, r)
			return
		}

		redirectToHTTPS(w, r)
	
	})

}


func processSvg(svg0 string, q url.Values) string {

	width := q.Get("w")
	height := q.Get("h")
	fill := q.Get("f")
	
	// 
	if height == "" && width != "" {
		height = width
	}
	if width == "" && height != "" {
		width = height
	}

	// Pseudo-parse the SVG and replace width, height, and fill
	// Default setting downloaded from google material icons
	svg1 := ""
	lines := strings.Split(svg0, "\n")
	for _, line := range lines {
		if strings.Contains(line, "<svg") {
			if width != "" {
				line = strings.Replace(line, `width="24px"`, `width="`+width+`"`, 1)
			}
			if height != "" {
				line = strings.Replace(line, `height="24px"`, `height="`+height+`"`, 1)
			}
			if fill != "" {
				line = strings.Replace(line, `fill="#5f6368"`, `fill="`+fill+`"`, 1)
			}
		}
		svg1 = svg1 + line + "\n"
	}
	return svg1

}

func staticHandler(w http.ResponseWriter, r *http.Request) {

	p := r.URL.Path
	if len(p) > 0 {
		p = p[1:]
	}

	// Handle special cases
	if p == "" {
		p = "static/index.html"
	}

	// Cache setter
	setCacheHeader := func() {
		// Cache max-age for selected static files
		if strings.HasPrefix(p, "static/ffmpeg/") {
			w.Header().Set("Cache-Control", "public, max-age=604800")
		} else {
			w.Header().Set("Cache-Control", "public, no-cache")
		}
	}

	// If gzipped static
	staticPath := p
	_, gzipped := gEmbedStaticEtags[p+".gz"]
	if gzipped {
		staticPath += ".gz"
	}

	// Get ETag
	etag, ok := gEmbedStaticEtags[staticPath]
	if !ok {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
    w.Header().Set("ETag", etag)
	
	// Check If-None-Match header
	if r.Header.Get("If-None-Match") == etag {
		setCacheHeader()
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// Write appropriate data
	ext := filepath.Ext(p)

	var data []byte
	if gzipped {

		w.Header().Set("Content-Encoding", "gzip")
		data, _ = gEmbedStatic.ReadFile(staticPath)

	} else {

		data, _ = gEmbedStatic.ReadFile(p)

		if ext == ".svg" {
			str := string(data)
			str  = processSvg(str, r.URL.Query())
			data = []byte(str)
		}

	}

	setCacheHeader()
	http.ServeContent(w, r, filepath.Base(p), time.Time{}, bytes.NewReader(data))

}


func populateEmbedEtags() {
	gEmbedStaticEtags = make(map[string]string)

	// WalkDir for traversing the embedded directory recursively
	err := fs.WalkDir(gEmbedStatic, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("failed to access path %s: %w", path, err)
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Gzip or general
		var etag string
		if strings.HasSuffix(path, ".gz") {

			sha1, err := gEmbedStatic.ReadFile(path[:len(path)-3] + ".sha1")
			if err != nil {
				return fmt.Errorf("cannot find sha1 sum for gzipped file: %w", err)
			}
			etag = fmt.Sprintf("\"%s\"", sha1)

		} else {

			// Read file content
			data, err := gEmbedStatic.ReadFile(path)
			if err != nil {
				return fmt.Errorf("error reading file %s: %w", path, err)
			}
			// Compute and store ETag
			etag = fmt.Sprintf("\"%08x\"", getCRC32OfBytes(data))

		}

		gEmbedStaticEtags[path] = etag
		return nil
	})

	if err != nil {
		logFatal(fmt.Errorf("failed to populate embedded ETags: %w", err))
	}
}

