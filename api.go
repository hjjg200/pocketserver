package main

import (
	"fmt"
	"net/http"
)

var apiMux *http.ServeMux

func init() {
	apiMux = http.NewServeMux()
	apiMux.HandleFunc("/api/typeByName", apiTypeByName)
}

func apiTypeByName(w http.ResponseWriter, r *http.Request) {
	mt := mimeTypeByName(r.URL.Query().Get("name"))
	if mt == "" {
		mt = "application/octet-stream"
	}
	fmt.Fprint(w, mt)
}