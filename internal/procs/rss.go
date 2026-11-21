package procs

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Mem is one of this user's processes with its resident memory.
type Mem struct {
	PID, PPID int
	// Started is when it started, from ps's elapsed time (to the second).
	Started time.Time
	RSSKB   int64
	Command string
}

// psMem is `ps -axww -o pid=,ppid=,uid=,etime=,rss=,command=`; a var for
// tests.
var psMem = func() ([]byte, error) {
	return exec.Command("ps", "-axww", "-o", "pid=", "-o", "ppid=", "-o", "uid=", "-o", "etime=", "-o", "rss=", "-o", "command=").Output()
}

// Mems lists this user's processes with their RSS, read at now.
func Mems(now time.Time) ([]Mem, error) {
	out, err := psMem()
	if err != nil {
		return nil, err
	}
	return parseMems(string(out), os.Getuid(), now), nil
}

func parseMems(out string, uid int, now time.Time) []Mem {
	var ms []Mem
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		u, err3 := strconv.Atoi(f[2])
		rss, err4 := strconv.ParseInt(f[4], 10, 64)
		age, ok := ParseEtime(f[3])
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || !ok || u != uid {
			continue
		}
		ms = append(ms, Mem{PID: pid, PPID: ppid, Started: now.Add(-age).Truncate(time.Second), RSSKB: rss, Command: strings.Join(f[5:], " ")})
	}
	return ms
}
