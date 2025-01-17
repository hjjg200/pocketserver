// +build linux
package main

import (
    "encoding/binary"
    "fmt"
    "golang.org/x/sys/unix"
    "unsafe"
)

// ifconf is used to pack/unpack the result of SIOCGIFCONF.
type ifconf struct {
    Len int32
    Ptr uintptr
}

// ifreq is one entry (interface) from SIOCGIFCONF; it has room for
// a sockaddr, but the size can vary by platform.
type ifreq struct {
    Name [unix.IFNAMSIZ]byte
    // The raw struct sockaddr in memory (we'll interpret only the family + bytes).
    Addr [unix.SizeofSockaddrInet6]byte
}

// parseSockaddr pulls out family and IP bytes from the raw ifreq.Addr.
func parseSockaddr(raw [unix.SizeofSockaddrInet6]byte) (string, error) {
    // The first 2 bytes are the sa_family
    family := binary.LittleEndian.Uint16(raw[:2]) // or BigEndian on some arch
    switch family {
    case unix.AF_INET:
        // For AF_INET, bytes 4..7 are the IP, 2..4 is the port
        ip := raw[4:8]
        return fmt.Sprintf("%d.%d.%d.%d", ip[0], ip[1], ip[2], ip[3]), nil
    case unix.AF_INET6:
        // For AF_INET6, bytes 8..23 are the IP, 6..8 is the port
        ip := raw[8:24]
        return fmt.Sprintf("%x:%x:%x:%x:%x:%x:%x:%x",
            uint16(ip[0])<<8|uint16(ip[1]),
            uint16(ip[2])<<8|uint16(ip[3]),
            uint16(ip[4])<<8|uint16(ip[5]),
            uint16(ip[6])<<8|uint16(ip[7]),
            uint16(ip[8])<<8|uint16(ip[9]),
            uint16(ip[10])<<8|uint16(ip[11]),
            uint16(ip[12])<<8|uint16(ip[13]),
            uint16(ip[14])<<8|uint16(ip[15]),
        ), nil
    default:
        // E.g. AF_PACKET or anything else
        return "", fmt.Errorf("unknown family: %d", family)
    }
}

// listAllAddresses uses ioctl(SIOCGIFCONF) to enumerate IP addresses, without net.Interfaces().
func listAllAddresses() ([]string, error) {
    // We make a large enough buffer to hold many interfaces; each ifreq can be up to
    // unix.SizeofSockaddrInet6 + IFNAMSIZ in size. For safety, pick a big number.
    const bufSize = 1024
    buf := make([]byte, bufSize)

    // Prepare the ifconf struct
    ifc := ifconf{
        Len: int32(bufSize),
        Ptr: uintptr(unsafe.Pointer(&buf[0])),
    }

    // Perform the IOCTL
    _, _, errno := unix.Syscall(
        unix.SYS_IOCTL,
        uintptr(unix.Stdin),
        uintptr(unix.SIOCGIFCONF),
        uintptr(unsafe.Pointer(&ifc)),
    )
    if errno != 0 {
        return nil, fmt.Errorf("ioctl SIOCGIFCONF failed: %v", errno)
    }

    // ifc.Len is now the number of bytes used in buf.
    used := int(ifc.Len)
    if used > bufSize {
        used = bufSize // should never happen
    }

    // Each record is an ifreq. The size of each ifreq can vary slightly on different OS/arch.
    // On Linux/amd64, an ifreq is typically:
    //    16 bytes for Name[] + 8 bytes of alignment + 28 bytes of sockaddr_in6? 
    // It's simpler to proceed 1 ifreq at a time carefully.
    var results []string
    pos := 0
    for pos+unix.IFNAMSIZ+unix.SizeofSockaddrInet6 <= used {
        // Interpret the memory as an ifreq
        var entry ifreq
        copy(entry.Name[:], buf[pos:pos+unix.IFNAMSIZ])
        copy(entry.Addr[:], buf[pos+unix.IFNAMSIZ:pos+unix.IFNAMSIZ+unix.SizeofSockaddrInet6])

        // Parse out IP
        ipStr, err := parseSockaddr(entry.Addr)
        if err == nil && ipStr != "" {
            // Trim the interface name
            ifname := string(entry.Name[:])
            ifname = ifname[:len(ifname)]
            // We can zero-trim the trailing '\x00'
            for i := range ifname {
                if ifname[i] == 0 {
                    ifname = ifname[:i]
                    break
                }
            }
            results = append(results, fmt.Sprintf("%s -> %s", ifname, ipStr))
        }

        // Move to the next ifreq
        pos += unix.IFNAMSIZ + unix.SizeofSockaddrInet6
    }

    return results, nil
}

func main() {
    addrs, err := listAllAddresses()
    if err != nil {
        panic(err)
    }
    for _, a := range addrs {
        fmt.Println(a)
    }
}

