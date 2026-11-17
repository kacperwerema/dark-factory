package procs

import (
	"os"
	"strconv"
	"strings"
)

func readEnv(pid int) ([]string, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil || len(b) == 0 {
		return nil, ErrNoEnv
	}
	var env []string
	for _, kv := range strings.Split(string(b), "\x00") {
		if kv != "" {
			env = append(env, kv)
		}
	}
	return env, nil
}
