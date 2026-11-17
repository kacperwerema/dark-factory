//go:build !darwin

package diskuse

import (
	"os"
	"time"
)

// birthKnown: stat keeps no birth time here (Linux has it only in statx).
const birthKnown = false

func birth(os.FileInfo) (time.Time, bool) { return time.Time{}, false }
