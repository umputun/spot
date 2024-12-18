// Copyright 2024 The Libc Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package libc // import "modernc.org/libc"

import (
	"syscall"
	"unsafe"
)

func Xpread(tls *TLS, fd int32, buf uintptr, size Tsize_t, ofs Toff_t) (r Tssize_t) {
	if __ccgo_strace {
		trc("tls=%v fd=%v buf=%v size=%v ofs=%v, (%v:)", tls, fd, buf, size, ofs, origin(2))
		defer func() { trc("-> %v", r) }()
	}
	n, err := syscall.Pread(int(fd), unsafe.Slice((*byte)(unsafe.Pointer(buf)), size), ofs)
	if err != nil {
		*(*int32)(unsafe.Pointer(X__errno_location(tls))) = int32(err.(syscall.Errno))
		return -1
	}

	return Tssize_t(n)
}

func Xpwrite(tls *TLS, fd int32, buf uintptr, size Tsize_t, ofs Toff_t) (r Tssize_t) {
	if __ccgo_strace {
		trc("tls=%v fd=%v buf=%v size=%v ofs=%v, (%v:)", tls, fd, buf, size, ofs, origin(2))
		defer func() { trc("-> %v", r) }()
	}
	n, err := syscall.Pwrite(int(fd), unsafe.Slice((*byte)(unsafe.Pointer(buf)), size), ofs)
	if err != nil {
		*(*int32)(unsafe.Pointer(X__errno_location(tls))) = int32(err.(syscall.Errno))
		return -1
	}

	return Tssize_t(n)
}
