//go:build !unix

package bench

import "os/exec"

func ownGroup(*exec.Cmd) {}
