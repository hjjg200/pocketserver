// +build linux,386
// linux and 386
// build for iSH

package main

// https://github.com/ish-app/ish/issues/1889#issuecomment-2143043997

/*
#cgo CFLAGS: -Wno-error
#include <arpa/inet.h>

int native_accept(int server_fd, struct sockaddr_in *address,
				  socklen_t *addrlen) {
  return accept(server_fd, (struct sockaddr *)address, addrlen);
}

int native_accept4(int server_fd, struct sockaddr_in * address,
				  socklen_t *addrlen, int flags) {
  return accept4(server_fd, (struct sockaddr *)address, addrlen, flags);
}
*/
import "C"

// CC=i686-linux-musl-gcc CGO_ENABLED=1 GOOS=linux GOARCH=386 go build -ldflags="-s -linkmode external -extldflags '-static' -checklinkname=0" -o imageserver_ish imageserver.go ish_fixer.go

import (
	"syscall"
	"unsafe"
	"runtime"
)

//go:linkname pollAcceptFunc internal/poll.AcceptFunc
var pollAcceptFunc func(int) (int, syscall.Sockaddr, error)

//go:linkname pollAccept4Func internal/poll.Accept4Func
var pollAccept4Func func(int, int) (int, syscall.Sockaddr, error)

//go:linkname syscallAnyToSockaddr syscall.anyToSockaddr
func syscallAnyToSockaddr(rsa *syscall.RawSockaddrAny) (syscall.Sockaddr, error)

func init() {
	pollAcceptFunc = Accept
	pollAccept4Func = Accept4

	// Max 1
	runtime.GOMAXPROCS(1)
}

// Replace [internal/poll.AcceptFunc] to fix `accept: function not implemented`
// error on iSH
func Accept(fd int) (nfd int, sa syscall.Sockaddr, err error) {
	var addr syscall.RawSockaddrAny
	var addrLen C.socklen_t = syscall.SizeofSockaddrAny

	newFd, err := C.native_accept(
		C.int(fd),
		(*C.struct_sockaddr_in)(unsafe.Pointer(&addr)),
		&addrLen,
	)
	if newFd < 0 {
		return -1, nil, err
	}

	sa, err = syscallAnyToSockaddr(&addr)
	if err != nil {
		return -1, nil, err
	}

	return int(newFd), sa, nil
}

// Replace [internal/poll.Accept4Func] to fix
// `accept4: function not implemented` error on iSH
func Accept4(fd, flags int) (nfd int, sa syscall.Sockaddr, err error) {
	var addr syscall.RawSockaddrAny
	var addrLen C.socklen_t = syscall.SizeofSockaddrAny

	newFd, err := C.native_accept4(
		C.int(fd),
		(*C.struct_sockaddr_in)(unsafe.Pointer(&addr)),
		&addrLen,
		C.int(flags),
	)
	if newFd < 0 {
		return -1, nil, err
	}

	sa, err = syscallAnyToSockaddr(&addr)
	if err != nil {
		return -1, nil, err
	}

	return int(newFd), sa, nil
}