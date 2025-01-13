package main

import (
	"fmt"
	"io"
	"net/http"
)

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	// Parse the multipart form
	err := r.ParseMultipartForm(10 << 20) // Limit memory to 10 MB
	if err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		fmt.Println("Error parsing form:", err)
		return
	}

	// Process uploaded files
	form := r.MultipartForm
	for _, files := range form.File {
		for _, fileHeader := range files {
			fmt.Println("Uploaded file:", fileHeader.Filename)

			// Open the file (discarding its content)
			file, err := fileHeader.Open()
			if err != nil {
				fmt.Println("Error opening file:", err)
				continue
			}

			// Discard the file content
			_, err = io.Copy(io.Discard, file)
			if err != nil {
				fmt.Println("Error discarding file content:", err)
			}

			// Close the file
			file.Close()
		}
	}

	fmt.Fprintln(w, "File upload processed successfully!")
}

func main() {
	http.HandleFunc("/upload", uploadHandler)
	fmt.Println("Server is running on http://localhost")
	http.ListenAndServe(":80", nil)
}
