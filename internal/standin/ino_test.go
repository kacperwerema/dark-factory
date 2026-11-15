package standin

import (
	"os"
	"syscall"
)

func statIno(fi os.FileInfo) uint64 { return uint64(fi.Sys().(*syscall.Stat_t).Ino) }
