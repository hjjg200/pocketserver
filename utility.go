
package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"path/filepath"
	"os"
	"sync/atomic"
	"time"
)

type Semaphore struct{
	ch chan struct{}
	timeout time.Duration
}
func NewSemaphore(n int, timeout time.Duration) *Semaphore {
	return &Semaphore{
		ch: make(chan struct{}, n),
		timeout: timeout,
	}
}
func (sem Semaphore) Acquire() bool {
	if sem.timeout > 0 {
		select {
		case sem.ch <- struct{}{}:
			return true
		case <-time.After(sem.timeout):
			return false
		}
	}
	
	sem.ch <- struct{}{}
	return true
}
func (sem Semaphore) Release() {
	<-sem.ch
}

// Independant functions

func mustJsonMarshal(data interface{}) string {
	j, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return string(j)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mustFilepathRel(base, target string) string {
	r, err := filepath.Rel(base, target)
	if err != nil {
		panic(err)
	}
	return r
}

// ChangeToExecDir changes the current working directory to the directory where the executable resides
func changeToExecDir() error {
	// Get the absolute path of the executable
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}

	// Get the directory of the executable
	execDir := filepath.Dir(execPath)

	// Change the working directory to the executable's directory
	if err := os.Chdir(execDir); err != nil {
		return fmt.Errorf("failed to change directory to %s: %v", execDir, err)
	}

	return nil
}

type rootCACertificate struct {
	cert *x509.Certificate
	key crypto.PrivateKey
}

func generateSelfSignedCert(root *rootCACertificate, certPath, keyPath, addr string) (err error) {

	// Generate private key
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %v", err)
	}

	// Serial
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %v", err)
	}
	
	// Generate a self-signed certificate
	template := &x509.Certificate{
		SerialNumber:		   serialNumber,
		NotBefore:             time.Now(),
		BasicConstraintsValid: true,
	}

	var certDER []byte
	// If this is root cert
	logDebug(root)
	if root == nil {

		template.KeyUsage 		= x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign
		template.IsCA			= true

		template.NotAfter		= time.Now().Add(10 * 365 * 24 * time.Hour)
		template.Subject		= pkix.Name{Organization: []string{"localhost pocketserver ROOT CA"}}

		certDER, err = x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
		if err != nil {
			return fmt.Errorf("failed to create certificate: %v", err)
		}

	} else {

		template.KeyUsage 		= x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature
		template.ExtKeyUsage 	= []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.IsCA 			= false
		
		template.NotAfter		= time.Now().Add(90 * 24 * time.Hour)
		template.Subject		= pkix.Name{
			CommonName:   addr, // Set Common Name to the provided address
			Organization: []string{"pocketserver"},
		}
		
		template.DNSNames		= []string{"localhost"} // Add localhost as a valid domain
		template.IPAddresses	= []net.IP{net.ParseIP("127.0.0.1")} // Add 127.0.0.1 as a valid IP

		if ip := net.ParseIP(addr); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, addr)
		}

		certDER, err = x509.CreateCertificate(rand.Reader, template, root.cert, &privateKey.PublicKey, root.key)
		if err != nil {
			return fmt.Errorf("failed to create certificate: %v", err)
		}
		
	}

	// Encode the certificate as PEM
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	// Encode the private key as PEM
	keyBytes, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	// Write key.pem to the current directory
	err = os.WriteFile(keyPath, keyPEM, 0600) // Use 0600 permissions for security
	if err != nil {
		return fmt.Errorf("Error writing key pem: %v", err)
	}

	// Write cert.pem to the current directory
	err = os.WriteFile(certPath, certPEM, 0644) // Readable by others if needed
	if err != nil {
		return fmt.Errorf("Error writing cert pem: %v", err)
	}

	// Write crt file for convenience
	if root == nil {
		err = os.WriteFile(certPath + ".crt", certPEM, 0644)
		if err != nil {
			return fmt.Errorf("Error writing cert pem.crt: %v", err)
		}
	}

	return nil

}

func loadRootCA(certPath, keyPath string) (*rootCACertificate) {

	root, err := _loadRootCA(certPath, keyPath)
	if err != nil {

		err = generateSelfSignedCert(nil, certPath, keyPath, "")
		if err != nil {
			panic(err)
		}

		root, err = _loadRootCA(certPath, keyPath)
		if err != nil {
			panic(err)
		}

	}

	return root

}

func _loadRootCA(certPath, keyPath string) (*rootCACertificate, error) {

	// Load certificate
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read root certificate: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("invalid root certificate file")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse root certificate: %v", err)
	}

	// Load private key
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read root key: %v", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil || keyBlock.Type != "EC PRIVATE KEY" {
		return nil, fmt.Errorf("invalid root key file")
	}
	privateKey, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse root private key: %v", err)
	}

	return &rootCACertificate{cert, privateKey}, nil
}


func ensureTLSCertificate(certPath, keyPath, addr string) {
	err := _ensureTLSCertificate(certPath, keyPath, addr)
	if err != nil {
		panic(err)
	}
}

func _ensureTLSCertificate(certPath, keyPath, addr string) error {

	//
	root := loadRootCA(ROOT_CERT_PEM, ROOT_KEY_PEM)

	// Load the certificate file
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Certificate doesn't exist, generate a new one
			logInfo("Certificate not found. Generating a new one.")
			return generateSelfSignedCert(root, certPath, keyPath, addr)
		}
		return fmt.Errorf("failed to read cert file: %v", err)
	}

	// Decode the PEM to parse the certificate
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return fmt.Errorf("invalid certificate file")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %v", err)
	}

	// Check expiration
	if time.Now().After(cert.NotAfter) {
		logInfo("Certificate expired. Regenerating...")
		return generateSelfSignedCert(root, certPath, keyPath, addr)
	}

	// Check if the addr is in the certificate's IPAddresses or DNSNames
	isValid := false
	ip := net.ParseIP(addr) // Try to parse addr as an IP
	if ip != nil {
		// Check IPAddresses field
		for _, certIP := range cert.IPAddresses {
			if certIP.Equal(ip) {
				isValid = true
				break
			}
		}
	} else {
		// Check DNSNames field
		for _, dnsName := range cert.DNSNames {
			if dnsName == addr {
				isValid = true
				break
			}
		}
	}
	if !isValid {
		logInfo("Certificate doesn't have local address included. Regenerating...")
		return generateSelfSignedCert(root, certPath, keyPath, addr)
	}

	// Optionally check if the certificate is close to expiration
	if time.Until(cert.NotAfter) < 7*24*time.Hour {
		logInfo("Certificate is close to expiration. Regenerating...")
		return generateSelfSignedCert(root, certPath, keyPath, addr)
	}

	return nil
}

func throttleAtomic(fn func(), delay time.Duration) func() {
	var lastCall atomic.Pointer[time.Time]

	return func() {
		now := time.Now()
		last := lastCall.Load()

		if last == nil || now.Sub(*last) >= delay {
			newLast := &now
			if lastCall.CompareAndSwap(last, newLast) {
				fn()
			}
		}
	}
}

// getOutboundIPs retrieves the preferred outbound IP address
func getOutboundIPs() (string, string) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	remoteAddr := conn.RemoteAddr().(*net.UDPAddr)
	return localAddr.IP.String(), remoteAddr.IP.String()
}



// FORMATTING

func generateRandomString(length int) (string, error) {
	// Create a byte slice of half the requested length (each byte -> 2 hex chars)
	bytes := make([]byte, length/2)
	_, err := rand.Read(bytes)
	if err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %v", err)
	}
	return hex.EncodeToString(bytes), nil
}

func formatShortTimestamp(since time.Time, at time.Time) string {
	
	elapsed := at.Sub(since)

	// Extract hours, minutes, and seconds from the duration
	totalSeconds := int(elapsed.Seconds())
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60

	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)

}
