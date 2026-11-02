package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stall blocks like a hung CaptureScreenshot until its attempt ends.
func stall(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func shortShots(t *testing.T) {
	t.Helper()
	prevT, prevN := shotTimeout, shotTries
	shotTimeout, shotTries = 50*time.Millisecond, 2
	t.Cleanup(func() { shotTimeout, shotTries = prevT, prevN })
}

func TestCaptureBoundedRetriesAStall(t *testing.T) {
	shortShots(t)
	calls := 0
	png, err := captureBounded(context.Background(), func(ctx context.Context) ([]byte, error) {
		calls++
		if calls == 1 {
			return stall(ctx)
		}
		return []byte("png"), nil
	})
	if err != nil || string(png) != "png" || calls != 2 {
		t.Fatalf("got %q, %v after %d calls; want the second try's shot", png, err, calls)
	}
}

func TestCaptureBoundedFailsClearlyInsteadOfHanging(t *testing.T) {
	shortShots(t)
	// The step's own deadline is far away: only the per-try bound stops it.
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	calls := 0
	_, err := captureBounded(ctx, func(ctx context.Context) ([]byte, error) {
		calls++
		return stall(ctx)
	})
	if err == nil || calls != 2 {
		t.Fatalf("err %v after %d calls; want a failure after 2", err, calls)
	}
	for _, want := range []string{"screenshot failed after 2 tries", "timed out after 50ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
}

func TestCaptureBoundedStopsWhenTheStepEnds(t *testing.T) {
	shortShots(t)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := captureBounded(ctx, func(context.Context) ([]byte, error) {
		calls++
		cancel()
		return nil, errors.New("target closed")
	})
	if err == nil || calls != 1 {
		t.Fatalf("err %v after %d calls; want one try once the step's context ended", err, calls)
	}
}

// TestChromePathNoneTurnsTheBrowserOff: the public CI sets
// PLAYTEST_CHROME=none, so every test that asks ChromePath skips there even
// though the runner has Chrome installed.
func TestChromePathNoneTurnsTheBrowserOff(t *testing.T) {
	t.Setenv("PLAYTEST_CHROME", "none")
	p, err := ChromePath()
	if err == nil || p != "" || !strings.Contains(err.Error(), "PLAYTEST_CHROME=none") {
		t.Fatalf("ChromePath() = %q, %v; want no path and an error naming PLAYTEST_CHROME=none", p, err)
	}
	t.Setenv("PLAYTEST_CHROME", "/opt/chrome")
	if p, err := ChromePath(); err != nil || p != "/opt/chrome" {
		t.Fatalf("ChromePath() = %q, %v; want the path PLAYTEST_CHROME names", p, err)
	}
}

// TestPublicCITurnsChromeOff: ci.yml must keep setting PLAYTEST_CHROME=none
// for go test, or the runner's installed Chrome runs every browser test.
func TestPublicCITurnsChromeOff(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoDir(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "PLAYTEST_CHROME: none") {
		t.Error(".github/workflows/ci.yml does not set PLAYTEST_CHROME: none for go test")
	}
}
