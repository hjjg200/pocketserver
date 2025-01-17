package main

import (
	"context"
	"fmt"
	"net"
)

// Simulated function to resolve well-known IPs and local addresses
func resolveWellKnownIPs(ctx context.Context, port int) {
	var wellKnownIPs = map[string]net.IP{
		"IPv4bcast":     net.IPv4(255, 255, 255, 255),
		"IPv4allsys":    net.IPv4(224, 0, 0, 1),
		"IPv4allrouter": net.IPv4(224, 0, 0, 2),
		"IPv4zero":      net.IPv4(0, 0, 0, 0),
		"IPv6zero":                   net.IP{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"IPv6unspecified":            net.IP{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"IPv6loopback":               net.IP{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		"IPv6interfacelocalallnodes": net.IP{0xff, 0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01},
		"IPv6linklocalallnodes":      net.IP{0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01},
		"IPv6linklocalallrouters":    net.IP{0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x02},
	}

	for name, ip := range wellKnownIPs {
		udpAddr := &net.UDPAddr{IP: ip, Port: port}
		conn, err := net.DialUDP("udp", nil, udpAddr)
		if err != nil {
			fmt.Printf("%s: Error connecting: %v\n", name, err)
			continue
		}
		defer conn.Close()

		localAddr := conn.LocalAddr().(*net.UDPAddr)
		fmt.Printf("%s: Local IP -> %s\n", name, localAddr.IP)
	}
}

func main() {
	ctx := context.Background()

	// Resolve well-known IPs on port 9
	resolveWellKnownIPs(ctx, 9)
}
