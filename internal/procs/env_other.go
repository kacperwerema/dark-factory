//go:build !darwin && !linux

package procs

func readEnv(pid int) ([]string, error) { return nil, ErrNoEnv }
