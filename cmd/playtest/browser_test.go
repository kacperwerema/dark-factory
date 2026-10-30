package main

import (
	"context"
	"errors"
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
