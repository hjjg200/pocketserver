package main

import (
    "fmt"
    "io"
    "net/http"
    "strconv"
    "time"
    "log"

    "crypto/ecdsa"
    "crypto/elliptic"
    "crypto/rand"
    "crypto/tls"
    "crypto/x509"
    "crypto/x509/pkix"
    "encoding/pem"
    "math/big"
)

func calculateThroughput(duration time.Duration, sizeMB int) string {
	// Convert duration to seconds
	seconds := duration.Seconds()

	// Calculate throughput in MB/s
	throughput := float64(sizeMB) / seconds

	return fmt.Sprintf("%.2f MB/s", throughput)
}


func generateSelfSignedCert() (certPEM []byte, keyPEM []byte, err error) {
    // Generate private key
    privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
    if err != nil {
        return nil, nil, fmt.Errorf("failed to generate private key: %v", err)
    }

    // Generate a self-signed certificate
    template := &x509.Certificate{
        SerialNumber: big.NewInt(1),
        Subject: pkix.Name{
            Organization: []string{"local network"},
        },
        NotBefore:             time.Now(),
        NotAfter:              time.Now().Add(24 * time.Hour), // Valid for 1 day
        KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
        ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
        BasicConstraintsValid: true,
    }

    certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
    if err != nil {
        return nil, nil, fmt.Errorf("failed to create certificate: %v", err)
    }

    // Encode the certificate as PEM
    certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

    // Encode the private key as PEM
    keyBytes, err := x509.MarshalECPrivateKey(privateKey)
    if err != nil {
        return nil, nil, fmt.Errorf("failed to marshal private key: %v", err)
    }
    keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

    return certPEM, keyPEM, nil
}


func main() {

    mux := http.NewServeMux()
    mux.HandleFunc("/100-down", func(w http.ResponseWriter, r *http.Request) {
        // Set content headers
        w.Header().Set("Content-Type", "application/octet-stream")
        w.Header().Set("Content-Disposition", "attachment; filename=\"100mb_zeros.bin\"")
        w.Header().Set("Content-Length", strconv.Itoa(100*1024*1024)) // 100 MB

        // Write 100MB of zeros
        start := time.Now()
        zeroBlock := make([]byte, 1024*1024) // 1MB block of zeros
        for i := 0; i < 100; i++ {
            if _, err := w.Write(zeroBlock); err != nil {
                return // Exit if the connection is closed or an error occurs
            }
        }
        elapsed := time.Now().Sub(start)
        fmt.Println("Download", elapsed, calculateThroughput(elapsed, 100))

    })

    // Serve HTML page at GET /100-upload
    mux.HandleFunc("/100-up", func(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodGet {
            // Serve the HTML and JavaScript for uploading 100MB of zeros
            w.Header().Set("Content-Type", "text/html")
            fmt.Fprint(w, `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>100MB Upload</title>
</head>
<body>
    <h1>Upload 100MB of Zeros</h1>
    <button id="uploadBtn">Upload 100MB</button>
    <script>
        document.getElementById('uploadBtn').addEventListener('click', () => {
            alert("Upload starts now");
            const xhr = new XMLHttpRequest();
            xhr.open('POST', '/100-up', true);
            xhr.onload = () => {
                if (xhr.status === 200) {
                    alert('Upload complete!');
                } else {
                    alert('Upload failed: ' + xhr.statusText);
                }
            };
            xhr.onerror = () => {
                alert('Network error occurred during upload.');
            };

            // Create 100MB of zeros
            const zeros = new Uint8Array(100 * 1024 * 1024); // 100MB buffer
            xhr.send(zeros);
        });
    </script>
</body>
</html>
`)
        } else if r.Method == http.MethodPost {
            // Handle file upload at POST /100-up
            w.Header().Set("Content-Type", "text/plain")

            start := time.Now()
            io.Copy(io.Discard, r.Body) // Write all received data to io.Discard
            r.Body.Close()
            elapsed := time.Now().Sub(start)
            fmt.Println("Upload", elapsed, calculateThroughput(elapsed, 100))

            fmt.Fprint(w, "Upload received successfully.")
        } else {
            http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
        }
    })

    // Wrap
    mux2 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if the request is HTTPS
		if r.TLS != nil {
			fmt.Println("HTTPS ------")
		} else {
			fmt.Println("http -------")
		}

		// Call the next handler
		mux.ServeHTTP(w, r)
	})

    // Generate temporary SSL credentials
    certPEM, keyPEM, err := generateSelfSignedCert()
    if err != nil {
        panic(fmt.Sprintf("Error generating self-signed certificate: %v", err))
    }

    // Load the certificate and key
    cert, err := tls.X509KeyPair(certPEM, keyPEM)
    if err != nil {
        panic(fmt.Sprintf("Error loading X509 key pair: %v", err))
    }

    // Start the HTTP server
    server := &http.Server{
        Addr: ":80",
        Handler: mux2,
        ErrorLog: log.New(io.Discard, "", 0), // Discard https errors which pop up so frequently due to untrusted certs
    }
    serverTLS := &http.Server{
        Addr: ":443",
        Handler: mux2,
        TLSConfig: &tls.Config{
            Certificates: []tls.Certificate{cert},
        },
        ErrorLog: log.New(io.Discard, "", 0), // Discard https errors which pop up so frequently due to untrusted certs
    }

    go func() {
        server.ListenAndServe()
    }()

    // Start the server
    serverTLS.ListenAndServeTLS("", "")
}