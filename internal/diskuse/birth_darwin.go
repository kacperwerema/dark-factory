package diskuse

import (
	"os"
	"syscall"
	"time"
)

// birthKnown: macOS keeps each file's birth time.
const birthKnown = true

// birth is info's creation time.
func birth(info os.FileInfo) (time.Time, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec), true
}
