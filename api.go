package main

import (
	"io/ioutil"
	"fmt"
	"net/http"
	"encoding/json"
	"strings"
)

var apiMux *http.ServeMux

func init() {
	apiMux = http.NewServeMux()

	// Registered by others
	apiMux.HandleFunc("/api/performance", func (w http.ResponseWriter, r *http.Request) {
		if apiPerformance != nil {
			apiPerformance(w, r)
		}
	})

	// ---
	apiMux.HandleFunc("/api/typeByName", apiTypeByName)
	apiMux.HandleFunc("/api/manifest", makeApiManifest())
	apiMux.HandleFunc("/api/bakeMetadata", apiBakeMetadata)

}

// Registered by others
var apiPerformance http.HandlerFunc = nil

func apiTypeByName(w http.ResponseWriter, r *http.Request) {
	mt := mimeTypeByName(r.URL.Query().Get("name"))
	if mt == "" {
		mt = "application/octet-stream"
	}
	fmt.Fprint(w, mt)
}

var gApiManifest = struct{
	FFmpegInputLimit		int64		`json:"ffmpegInputLimit"`
}{
	FFmpegInputLimit:		1_073_741_824,
}
func makeApiManifest() http.HandlerFunc {
	d, err := json.Marshal(gApiManifest)
	must(err)
	return func (w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, string(d))
	}
}

func apiBakeMetadata(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(w, "Request is not POST", http.StatusMethodNotAllowed)
		return
	}
	
	info := struct{
		Album		string		`json:"album"`
		Base		string		`json:"base"`
		Commands	[]struct{
			Input		int		`json:"input"`
			Output		int		`json:"output"`
			OutputExt	string	`json:"outputExt"`
			Args		[]string`json:"args"`	
		}						`json:"commands"`
	}{}

	data, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Body cannot be read", http.StatusBadRequest)
		return
	}
	err = json.Unmarshal(data, &info)
	if err != nil {
		http.Error(w, "Body is not json", http.StatusBadRequest)
		return
	}

	// ---
	inFullpath := getUploadFullpath(info.Album, info.Base)
	if _, err := ioStat(inFullpath); err != nil {
		http.Error(w, "Input file is not found", http.StatusBadRequest)
		return
	}

	for _, cmd := range info.Commands {
		cmd.Args[cmd.Input] = inFullpath
		cmd.Args[cmd.Output] = getMetadataFullpath(info.Album, info.Base, cmd.OutputExt)
		logDebug(strings.Join(cmd.Args, " "))
		err = executeFFmpeg(cmd.Args, nil, nil)
		if err != nil {
			logHTTPRequest(r, -1, "failed to run native ffmpeg:", err)
			http.Error(w, "failed to run native ffmpeg", http.StatusInternalServerError)
			return
		}
	}

}