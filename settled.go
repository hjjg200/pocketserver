package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"sync"
	"runtime"
)


type AppInfo struct {
	Start time.Time
	LocalIP string // IPv4 string of local ip at startup
	UploadCount int // Total count of uploads since startup // TODO atomic
	UploadDir string
	MetadataDir string
	Debug bool
}

var gAppInfo AppInfo



func pingHandler(w http.ResponseWriter, r *http.Request) {
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
	fmt.Println(timestamp, logLine, strings.Join(strItems, " "))
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
	elapsed := time.Duration(0)
	if start, ok := r.Context().Value(CONTEXT_KEY_REQUEST_START).(time.Time); ok {
		elapsed = time.Since(start)
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
		"%s %s %s %s (%v) %s <-%s%s",
		rID, protocol, method, statusStr, elapsed, url, raddr, tail,
	)
	logTimestamp(logLine, items...)

}


func recursiveNewName(dir string, fn string) string {
	ext := filepath.Ext(fn)
	base := filepath.Base(fn)
	fn1 := fn

	for i := 0; i < 5; i++ {

		if i > 0 {
			fn1 = fmt.Sprintf("%s-%d%s", base, i+1, ext)
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
	return fmt.Sprintf("%s-%d%s", base, time.Now().Unix(), ext)
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
	EnablerRemoteIP		string // IPv4
	LocalIPAtEnabled	string
	RemoteIPAtEnabled	string
}
var gHTTPInfo HTTPInfo

func authMiddleware(next http.Handler) http.Handler {

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

		li, ri := getOutboundIPs()
		gHTTPInfo.LocalIPAtEnabled = li
		gHTTPInfo.RemoteIPAtEnabled = ri

		// Construct the HTTP URL for redirection
		httpURL := fmt.Sprintf("http://%s/", r.Host)

		// Enable HTTP
		gHTTPInfo.Enabled = true

		// Redirect to the HTTP root (/) of the same host
		logHTTPRequest(r, -1, "HTTP is temporarily enabled")
		http.Redirect(w, r, httpURL, http.StatusSeeOther)

	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		now := time.Now()

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

						// If cookie exists and is valid, pass request to the main handler
						//logHTTPRequest(r, -1, "VALID COOKIE")
						next.ServeHTTP(w, r)

						gAuthInfo.ExpiryMapMu.Unlock()
						return

					} else {
						logHTTPRequest(r, -1, "COOKIE EXPIRED")
						delete(gAuthInfo.ExpiryMap, v)
					}

				} else {
					logHTTPRequest(r, -1, "COOKIE EXPIRED")
				}

				err = storeAuthCookies()
				if err != nil {
					logHTTPRequest(r, -1, "failed to store auth cookies!")
				}

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
				
				err = storeAuthCookies()
				if err != nil {
					logHTTPRequest(r, -1, "failed to store auth cookies!")
				}
				gAuthInfo.ExpiryMapMu.Unlock()

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

	return http.HandlerFunc(func (w http.ResponseWriter, r *http.Request) {

		raddr, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			logHTTPRequest(r, -1, "ListenAndServe net.SplitHostPort err:", err)
			redirectToHTTPS(w, r)
			return
		}

		// Localhost privilege
		if raddr == "127.0.0.1" {
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

			li, ri := getOutboundIPs()
			if li != gHTTPInfo.LocalIPAtEnabled || ri != gHTTPInfo.RemoteIPAtEnabled {
				gHTTPInfo.Enabled = false
				logHTTPRequest(r, -1, "HTTP is disabled, li ri mismatch", "HTTP is disabled, li ri mismatch", gHTTPInfo.LocalIPAtEnabled, li, gHTTPInfo.RemoteIPAtEnabled, ri)
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
		svg1 = line + "\n"
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

	// Get ETag
	etag, ok := gEmbedStaticEtags[p]
	if !ok {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	
	// Check If-None-Match header
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	//
	var data []byte

	ext := filepath.Ext(p)
	data, _ = gEmbedStatic.ReadFile(p)
	if ext == ".svg" {
		str := string(data)
		str  = processSvg(str, r.URL.Query())
		data = []byte(str)
	}
	
    w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, no-cache")
	http.ServeContent(w, r, filepath.Base(p), time.Time{}, bytes.NewReader(data))

}

func populateEmbedEtags() {

	gEmbedStaticEtags = make(map[string]string)

	entries, err := gEmbedStatic.ReadDir("static")
    if err != nil {
        logFatal(fmt.Errorf("failed to read embedded directory: %w", err))
    }

    for _, entry := range entries {
        if !entry.IsDir() {
            // Read file content
            path := "static/" + entry.Name()
            data, err := gEmbedStatic.ReadFile(path)
            if err != nil {
                logFatal(fmt.Errorf("Error reading file %s: %w", path, err))
            }

            // Store the ETag (as a string) in the map
			etag := fmt.Sprintf("\"%x\"", getCRC32OfBytes(data))
            gEmbedStaticEtags[path] = etag
        }
    }

}
