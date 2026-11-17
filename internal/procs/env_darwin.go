package procs

import (
	"bytes"
	"encoding/binary"
	"syscall"
	"unsafe"
)

// readEnv reads kern.procargs2: argc (int32), the exec path, NUL padding,
// argc arguments, then the environment up to an empty string.
func readEnv(pid int) ([]string, error) {
	mib := []int32{1 /* CTL_KERN */, 49 /* KERN_PROCARGS2 */, int32(pid)}
	var size uintptr
	if _, _, e := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), 3, 0, uintptr(unsafe.Pointer(&size)), 0, 0); e != 0 {
		return nil, ErrNoEnv
	}
	if size < 4 {
		return nil, ErrNoEnv
	}
	buf := make([]byte, size)
	if _, _, e := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), 3, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, 0); e != 0 {
		return nil, ErrNoEnv
	}
	return parseProcArgs(buf[:size])
}

func parseProcArgs(b []byte) ([]string, error) {
	if len(b) < 4 {
		return nil, ErrNoEnv
	}
	argc := int(binary.LittleEndian.Uint32(b[:4]))
	b = b[4:]
	// exec path
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return nil, ErrNoEnv
	}
	b = b[i:]
	for len(b) > 0 && b[0] == 0 {
		b = b[1:]
	}
	for n := 0; n < argc; n++ {
		i := bytes.IndexByte(b, 0)
		if i < 0 {
			return nil, ErrNoEnv
		}
		b = b[i+1:]
	}
	var env []string
	for len(b) > 0 {
		i := bytes.IndexByte(b, 0)
		if i <= 0 {
			break
		}
		env = append(env, string(b[:i]))
		b = b[i+1:]
	}
	if len(env) == 0 {
		return nil, ErrNoEnv
	}
	return env, nil
}
