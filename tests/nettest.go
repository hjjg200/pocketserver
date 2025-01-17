package main

import (
	"context"
	"fmt"
	"net"
)

// Simulated function to resolve wildcard addresses and local IPs
func resolveLocalIPs(ctx context.Context, network, address string) ([]string, error) {
	var resolvedIPs []string

	// Split the address into host and port
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid address format: %w", err)
	}

	// Handle wildcard addresses explicitly
	if host == "" || host == "0.0.0.0" || host == "::" {
		// Include explicit wildcards
		resolvedIPs = append(resolvedIPs, net.JoinHostPort("0.0.0.0", port))
		resolvedIPs = append(resolvedIPs, net.JoinHostPort("::", port))

		// Add loopback and resolve localhost to cover common cases
		loopbackAddresses := []string{"127.0.0.1", "::1"}
		for _, loopback := range loopbackAddresses {
			resolvedIPs = append(resolvedIPs, net.JoinHostPort(loopback, port))
		}

		// Resolve any interface-bound addresses
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, "localhost")
		if err == nil {
			for _, ip := range ips {
				resolvedIPs = append(resolvedIPs, net.JoinHostPort(ip.IP.String(), port))
			}
		}
	} else {
		// If the host is not a wildcard, resolve it directly
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve host: %w", err)
		}

		for _, ip := range ips {
			resolvedIPs = append(resolvedIPs, net.JoinHostPort(ip.IP.String(), port))
		}
	}

	return resolvedIPs, nil
}

func main() {
	ctx := context.Background()

	// Example: Resolve local addresses for "tcp" network and wildcard address
	addresses, err := resolveLocalIPs(ctx, "tcp", ":80")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Println("Resolved Local Addresses:")
	for _, addr := range addresses {
		fmt.Println(addr)
	}
}
