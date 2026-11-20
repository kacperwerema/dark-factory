package viewer

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clean"
	"github.com/kacperwerema/dark-factory/internal/diskuse"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The nightly clean (issue-cli clean, internal/clean): each health tick
// asks whether it is due (no clean since tonight's clean.at, or the volume
// over 85% used and the last clean 6 h ago) and starts `issue-cli clean
// --if-due` detached, once for the machine. The command itself waits for
// workers, local CI and go builds, and records why; the next tick tries
// again.

// cleanSpawn starts the clean. A var so tests never start one.
var cleanSpawn = func(config string) error {
	exe, err := exec.LookPath("issue-cli")
	if err != nil {
		return fmt.Errorf("issue-cli is not on PATH (make install): %w", err)
	}
	args := []string{"clean", "--if-due"}
	if config != "" {
		args = append([]string{"--config", config}, args...)
	}
	cmd := exec.Command(exe, args...)
	if path := clean.Path(); path != "" {
		if f, err := os.OpenFile(filepath.Join(filepath.Dir(path), "clean.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			cmd.Stdout, cmd.Stderr = f, f
			defer f.Close()
		}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// cleanVolume is the data volume's use now: a var for tests.
var cleanVolume = func() diskuse.Volume {
	if dir := diskuse.GoCacheDir(); dir != "" {
		if v := diskuse.VolumeOf(dir); v.OK {
			return v
		}
	}
	home, _ := os.UserHomeDir()
	return diskuse.VolumeOf(home)
}

// cleanLive is false in a test binary and a scenario (diskuse.Live).
var cleanLive = func() bool { return diskuse.Live }

// cleanTick starts the clean when it is due; it returns why it started
// ("" when it didn't).
func cleanTick(projects []tracker.Project, now time.Time, statePath string, lastErr map[string]string) string {
	if !cleanLive() || len(projects) == 0 || statePath == "" {
		return ""
	}
	config := projects[0].ConfigPath
	var cfg *tracker.CleanConfig
	if config != "" {
		if c, err := tracker.LoadProjectsConfig(config); err == nil {
			cfg = c.Clean
		}
	}
	if !cfg.Enabled() {
		return ""
	}
	if _, running := clean.Running(statePath); running {
		return ""
	}
	h, m := cfg.Clock()
	due, why := clean.Due(clean.Load(statePath), h, m, cleanVolume(), now)
	if !due {
		delete(lastErr, "#clean-start")
		return ""
	}
	if err := cleanSpawn(config); err != nil {
		if msg := err.Error(); lastErr["#clean"] != msg {
			lastErr["#clean"] = msg
			log.Printf("clean: %v", err)
		}
		return ""
	}
	delete(lastErr, "#clean")
	// A held clean is started again every tick: log a start once per
	// reason.
	if lastErr["#clean-start"] != why {
		lastErr["#clean-start"] = why
		log.Printf("clean: started (%s)", why)
	}
	return why
}
