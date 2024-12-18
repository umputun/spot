// Copyright 2024 The Libc Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package libc // import "modernc.org/libc"

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

func Xpread(tls *TLS, fd int32, buf uintptr, size Tsize_t, ofs Toff_t) (r Tssize_t) {
	if __ccgo_strace {
		trc("tls=%v fd=%v buf=%v size=%v ofs=%v, (%v:)", tls, fd, buf, size, ofs, origin(2))
		defer func() { trc("-> %v", r) }()
	}
	n, err := unix.Pread(int(fd), unsafe.Slice((*byte)(unsafe.Pointer(buf)), size), ofs)
	if err != nil {
		*(*int32)(unsafe.Pointer(X__errno_location(tls))) = int32(err.(unix.Errno))
		return -1
	}

	return Tssize_t(n)
}

func Xpwrite(tls *TLS, fd int32, buf uintptr, size Tsize_t, ofs Toff_t) (r Tssize_t) {
	if __ccgo_strace {
		trc("tls=%v fd=%v buf=%v size=%v ofs=%v, (%v:)", tls, fd, buf, size, ofs, origin(2))
		defer func() { trc("-> %v", r) }()
	}
	n, err := unix.Pwrite(int(fd), unsafe.Slice((*byte)(unsafe.Pointer(buf)), size), ofs)
	if err != nil {
		*(*int32)(unsafe.Pointer(X__errno_location(tls))) = int32(err.(unix.Errno))
		return -1
	}

	return Tssize_t(n)
}

func Xftruncate(tls *TLS, fd int32, length Toff_t) (r int32) {
	if __ccgo_strace {
		trc("tls=%v fd=%v length=%v, (%v:)", tls, fd, length, origin(2))
		defer func() { trc("-> %v", r) }()
	}
	//return X__syscall_ret(tls, uint32(X__syscall2(tls, int32(SYS_ftruncate64), fd, int32(length))))
	if err := unix.Ftruncate(int(fd), length); err != nil {
		*(*int32)(unsafe.Pointer(X__errno_location(tls))) = int32(err.(unix.Errno))
		return -1
	}

	return 0
}

func _read(tls *TLS, fd int32, buf uintptr, count Tsize_t) (r Tssize_t) {
	return Xread(tls, fd, buf, count)
}

func Xread(tls *TLS, fd int32, buf uintptr, count Tsize_t) (r Tssize_t) {
	if __ccgo_strace {
		trc("tls=%v fd=%v buf=%v count=%v, (%v:)", tls, fd, buf, count, origin(2))
		defer func() { trc("-> %v", r) }()
	}
	// return X__syscall_ret(tls, uint32(___syscall_cp(tls, int32(SYS_read), fd, int32(buf), int32(count), 0, 0, 0)))
	n, err := unix.Read(int(fd), unsafe.Slice((*byte)(unsafe.Pointer(buf)), count))
	if err != nil {
		*(*int32)(unsafe.Pointer(X__errno_location(tls))) = int32(err.(unix.Errno))
		return -1
	}

	return Tssize_t(n)
}

func _write(tls *TLS, fd int32, buf uintptr, count Tsize_t) (r Tssize_t) {
	return Xwrite(tls, fd, buf, count)
}

func Xwrite(tls *TLS, fd int32, buf uintptr, count Tsize_t) (r Tssize_t) {
	if __ccgo_strace {
		trc("tls=%v fd=%v buf=%v count=%v, (%v:)", tls, fd, buf, count, origin(2))
		defer func() { trc("-> %v", r) }()
	}
	// return X__syscall_ret(tls, uint32(___syscall_cp(tls, int32(SYS_write), fd, int32(buf), int32(count), 0, 0, 0)))
	n, err := unix.Write(int(fd), unsafe.Slice((*byte)(unsafe.Pointer(buf)), count))
	if err != nil {
		*(*int32)(unsafe.Pointer(X__errno_location(tls))) = int32(err.(unix.Errno))
		return -1
	}

	return Tssize_t(n)
}
