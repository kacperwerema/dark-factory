//go:build darwin || linux

package diskuse

import "syscall"

// volumeOf is the use of path's file system as df prints it: used over
// used plus what a non-root user may still write.
func volumeOf(path string) Volume {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Volume{}
	}
	bs := uint64(st.Bsize)
	used := (uint64(st.Blocks) - uint64(st.Bfree)) * bs
	avail := uint64(st.Bavail) * bs
	if used+avail == 0 {
		return Volume{}
	}
	return Volume{UsedPct: float64(used) / float64(used+avail) * 100, FreeGB: float64(avail) / (1 << 30), OK: true}
}
