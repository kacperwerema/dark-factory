//go:build !darwin && !linux

package diskuse

func volumeOf(string) Volume { return Volume{} }
