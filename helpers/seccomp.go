package helpers

import (
	"fmt"
	"syscall"
	"unsafe"
)

func applySeccomp() {
	const PR_SET_NO_NEW_PRIVS = 38
	const PR_SET_SECCOMP = 22
	const SECCOMP_MODE_FILTER = 2

	var filter = []syscall.SockFilter{
		{Code: 0x20, Jt: 0, Jf: 0, K: 0},
		{Code: 0x15, Jt: 0, Jf: 1, K: 83},
		{Code: 0x06, Jt: 0, Jf: 0, K: 0x00050001},
		{Code: 0x06, Jt: 0, Jf: 0, K: 0x7FFF0000},
	}

	prog := &syscall.SockFprog{
		Len:    uint16(len(filter)),
		Filter: &filter[0],
	}

	_, _, errNo := syscall.RawSyscall6(syscall.SYS_PRCTL, PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0, 0)
	if errNo != 0 {
		panic(fmt.Errorf("prctl(PR_SET_NO_NEW_PRIVS) failed: %v", errNo))
	}
	_, _, errNo = syscall.RawSyscall6(syscall.SYS_PRCTL, PR_SET_SECCOMP, SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(prog)), 0, 0, 0)
	if errNo != 0 {
		panic(fmt.Errorf("prctl(PR_SET_SECCOMP) failed: %v", errNo))
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
