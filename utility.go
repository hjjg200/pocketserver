
package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"hash/crc32"
	"encoding/hex"
	"encoding/pem"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"path/filepath"
	"os"
	"sync/atomic"
	"strings"
	"time"
	"sort"
	"sync"
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

func generateSelfSignedCert(root *rootCACertificate, certPath, keyPath string, addrs []string) (err error) {

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
	if root == nil {

		template.KeyUsage 		= x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign
		//template.ExtKeyUsage
		template.IsCA			= true

		template.MaxPathLen		= 1           // Only allow one level of intermediate certs
		template.MaxPathLenZero	= false   // Enforce the path length constraint

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

		//template.MaxPathLen
		//template.MaxPathLenZero
		
		template.NotAfter		= time.Now().Add(90 * 24 * time.Hour)
		template.Subject		= pkix.Name{
			CommonName:   addrs[0], // Set Common Name to the provided address
			Organization: []string{"pocketserver"},
		}

		// Issued certificates only
		template.DNSNames		= []string{"localhost"} // Add localhost as a valid domain
		template.IPAddresses	= []net.IP{}
			//net.ParseIP("127.0.0.1"),	// Add 127.0.0.1 as a valid IP
			//net.ParseIP("::1")}			// Add ::1

		for _, addr := range addrs {
			if ip := net.ParseIP(addr); ip != nil {
				template.IPAddresses = append(template.IPAddresses, ip)
			} else {
				template.DNSNames = append(template.DNSNames, addr)
			}
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

		err = generateSelfSignedCert(nil, certPath, keyPath, nil)
		if err != nil {
			panic(err)
		}
		logError("ROOT certificate is newly issued. Remove all of the following: prior root certificate from your device's trusted list, previously used subordinate certificates and keys")

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


func ensureTLSCertificate(certPath, keyPath string, addrs []string) {
	err := _ensureTLSCertificate(certPath, keyPath, addrs)
	if err != nil {
		panic(err)
	}
}

func _ensureTLSCertificate(certPath, keyPath string, addrs []string) error {

	//
	root := loadRootCA(ROOT_CERT_PEM, ROOT_KEY_PEM)

	// Load the certificate file
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Certificate doesn't exist, generate a new one
			logInfo("Certificate not found. Generating a new one.")
			return generateSelfSignedCert(root, certPath, keyPath, addrs)
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

	if err = validateCertificate(cert, addrs); err != nil {
		logWarn("Certificate error", err,"Regenerating...")
		return generateSelfSignedCert(root, certPath, keyPath, addrs)
	}

	return nil
}
// validateCertificate checks the validity of a certificate against multiple addresses
func validateCertificate(cert *x509.Certificate, addrs []string) error {
    // Check if the certificate has expired
    if time.Now().After(cert.NotAfter) {
        return fmt.Errorf("certificate expired")
    }

    // Check if the certificate is expiring soon (less than 7 days remaining)
    if time.Until(cert.NotAfter) < 7*24*time.Hour {
        return fmt.Errorf("certificate expiring soon")
    }

	count := 0
    // Iterate over all provided addresses
    for _, addr := range addrs {
        ip := net.ParseIP(addr)
        if ip != nil {
            // Check if the IP address is in the certificate
            for _, certIP := range cert.IPAddresses {
                if certIP.Equal(ip) {
					count++
                }
            }
        } else {
            // Check if the DNS name is in the certificate
            for _, dnsName := range cert.DNSNames {
                if dnsName == addr {
					count++
                }
            }
        }
    }

	if count != len(addrs) {
		return fmt.Errorf("addresses are not found in the certificate")
	}

	return nil

}

// formatBytes converts bytes into a human-readable string with at most 3 significant digits,
// using early exits for specific thresholds.
func formatBytes(bytes int64) string {
	if bytes == 0 {
		return "0 B"
	}

	const k = int64(1024)

	// Early exits for specific thresholds
	if bytes < k {
		return fmt.Sprintf("%d %s", bytes, "B") // Bytes
	}
	if bytes < k*k {
		return fmt.Sprintf("%.3g %s", float64(bytes)/float64(k), "KB") // KB
	}
	if bytes < k*k*k {
		return fmt.Sprintf("%.3g %s", float64(bytes)/float64(k*k), "MB") // MB
	}
	if bytes < k*k*k*k {
		return fmt.Sprintf("%.3g %s", float64(bytes)/float64(k*k*k), "GB") // GB
	}

	// Default for TB and beyond
	return fmt.Sprintf("%.3g %s", float64(bytes)/float64(k*k*k*k), "TB") // TB
}


func throttle(fn func(), delay time.Duration) func() {
	
	if delay == 0 {
		return fn
	}

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

func debounce(fn func(), delay time.Duration) func() {

    if delay == 0 {
        // If no delay, just run the function immediately.
        return fn
    }

    var mu sync.Mutex
    var timer *time.Timer

    return func() {
        mu.Lock()
        defer mu.Unlock()

        // If a timer is already running, stop it so we can reset.
        if timer != nil {
            timer.Stop()
        }

        // Schedule a new timer that fires after `delay`.
        timer = time.AfterFunc(delay, fn)
    }

}

// isIPv4 checks if an IP address is IPv4
func isIPv4(address string) bool {
	ip := net.ParseIP(address)
	return ip != nil && ip.To4() != nil
}

// Simulated function to resolve well-known IPs and return unique local addresses
func resolveLocalIPs() ([]string) {
	var testedIPs = []net.IP{
		net.IPv4bcast,                 // IPv4 limited broadcast
		net.IPv4allsys,               // IPv4 all systems
		net.IPv4allrouter,            // IPv4 all routers
		net.IPv4zero,                 // IPv4 all zeros
		net.IPv6zero,                 // IPv6 all zeros
		net.IPv6unspecified,          // IPv6 unspecified
		net.IPv6loopback,             // IPv6 loopback
		net.IPv6interfacelocalallnodes, // IPv6 interface-local all nodes
		net.IPv6linklocalallnodes,    // IPv6 link-local all nodes
		net.IPv6linklocalallrouters,  // IPv6 link-local all routers
	}

	uniqueIPs := make(map[string]struct{}) // Map to track unique local IPs
	for _, ip := range testedIPs {
		udpAddr := &net.UDPAddr{IP: ip, Port: 9}
		conn, err := net.DialUDP("udp", nil, udpAddr)
		if err != nil {
			//fmt.Printf("Error connecting to %s: %v\n", ip, err)
			continue
		}
		defer conn.Close()

		localAddr := conn.LocalAddr().(*net.UDPAddr)
		uniqueIPs[localAddr.IP.String()] = struct{}{}
	}

	// Convert map keys to a slice
	result := []string{}
	for ip := range uniqueIPs {
		result = append(result, ip)
	}

	return result
}

// generateAddressesHash creates a hash based on addresses
func generateAddressesHash(addresses []string) string {
	cp := make([]string, len(addresses))
	copy(cp, addresses)
	sort.Strings(cp) // Ensure consistent order
	// Create a hash
	return getCRC32OfBytes([]byte(strings.Join(cp, ";")))
}

/*
func getLocalAddresses() (string, map[string][]string, error) {
	var addresses = make(map[string][]string)
	preferredInterface := ""

	// Get a list of all system interfaces
	interfaces, err := net.Interfaces()
	logDebug(err)
	if err != nil {
		return "", nil, err
	}

	// Collect all addresses (IPv4 and IPv6)
	for _, iface := range interfaces {
		// Skip interfaces that are down or don't support multicast
		if iface.Flags&(net.FlagUp|net.FlagMulticast) == 0 {
			continue
		}

		// Get a list of addresses for the interface
		addrs, err := iface.Addrs()
		logDebug(err)
		if err != nil {
			return "", nil, err
		}

		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}

			ipStr := ip.String()

			// Add the address to the result
			if _, ok := addresses[iface.Name]; !ok {
				addresses[iface.Name] = make([]string, 0)
			}

			//
			if ip.To4() != nil {
				addresses[iface.Name] = append([]string{ipStr}, addresses[iface.Name]...)
			} else {
				addresses[iface.Name] = append(addresses[iface.Name], ipStr)
			}

			// First interface that is not loopback
			if preferredInterface == "" && ipStr != "127.0.0.1" && ipStr != "::1"  {
				preferredInterface = iface.Name
			}
		}
	}

	return preferredInterface, addresses, nil
}

// generateInterfaceHash creates a hash based on the interface data.
func generateInterfaceHash(addresses map[string][]string) string {
	var data []string

	// Collect interface data into a sorted slice for consistent hashing
	for iface, addrs := range addresses {
		sort.Strings(addrs)
		data = append(data, fmt.Sprintf("%s:%s", iface, strings.Join(addrs, ",")))
	}
	sort.Strings(data) // Ensure consistent order

	// Create a hash
	return getCRC32OfBytes([]byte(strings.Join(data, ";")))
}

// getPreferredInterface determines the preferred interface by connecting to Google's public DNS
func getInternetInterface() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80") // Google's DNS (IPv4)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	// Get the local address used for the connection
	localAddr := conn.LocalAddr().(*net.UDPAddr)

	// Find the interface corresponding to the local IP
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range interfaces {
		// Skip interfaces that are down
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}

			// Match the local IP address
			if ip.Equal(localAddr.IP) {
				return iface.Name, nil
			}
		}
	}

	return "", fmt.Errorf("no matching interface found")

}*/


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

func getCRC32OfBytes(data []byte) string {
	crc32Hash := crc32.ChecksumIEEE(data)
	return fmt.Sprintf("%x", crc32Hash)
}