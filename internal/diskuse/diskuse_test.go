package diskuse

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeSized(t *testing.T, p string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Files born now count as created in the last 24 h on macOS; a reading
// for a day after they were made counts none of them.
func TestMeasureCountsNewCacheFilesByBirthTime(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("GOCACHE", cache)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	writeSized(t, filepath.Join(cache, "00", "00aa-d"), 2<<20)
	r, err := Measure(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !r.HasGoCache || r.GoCacheMB != 2 || r.HasTranscripts {
		t.Fatalf("reading = %+v", r)
	}
	if runtime.GOOS != "darwin" {
		if r.HasNew {
			t.Errorf("HasNew on %s, which keeps no birth time", runtime.GOOS)
		}
		return
	}
	if !r.HasNew || r.GoCacheNewMB != 2 {
		t.Errorf("new = %v (has %v), want 2", r.GoCacheNewMB, r.HasNew)
	}
	later, _ := Measure(context.Background(), time.Now().Add(25*time.Hour))
	if later.GoCacheNewMB != 0 {
		t.Errorf("a day later new = %v, want 0", later.GoCacheNewMB)
	}
}

func TestCheckLines(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	big := Reading{At: now.Add(-time.Hour), HasGoCache: true, GoCacheMB: 159 * 1024}
	ok := Volume{UsedPct: 80, FreeGB: 180, OK: true}
	full := Volume{UsedPct: 86, FreeGB: 120, OK: true}
	for _, c := range []struct {
		name string
		r    Reading
		has  bool
		vol  Volume
		over bool
		why  string
	}{
		{"big cache", big, true, ok, true, "the cache is over 100 GB"},
		{"full volume", Reading{}, false, full, true, "the volume is over 85% used"},
		{"both", big, true, full, true, "the cache is over 100 GB and the volume is over 85% used"},
		{"quiet", Reading{At: now, HasGoCache: true, GoCacheMB: 2048}, true, ok, false, ""},
		{"stale reading", Reading{At: now.Add(-4 * 24 * time.Hour), HasGoCache: true, GoCacheMB: 159 * 1024}, true, ok, false, ""},
	} {
		v := Check(c.r, c.has, c.vol, now)
		if v.Over != c.over || v.Why != c.why {
			t.Errorf("%s: over %v why %q, want %v %q", c.name, v.Over, v.Why, c.over, c.why)
		}
	}
	v := Check(big, true, ok, now)
	if v.Line != "the Go build cache holds 159 GB, the data volume is 80% used (180 GB free)" {
		t.Errorf("line = %q", v.Line)
	}
	if a := Advice(); !strings.Contains(a, Command) || !strings.Contains(a, "no worker is running") {
		t.Errorf("advice = %q", a)
	}
}

// In a test binary nothing reads the machine unless a test says so.
func TestNotLiveInTests(t *testing.T) {
	if Live {
		t.Fatal("Live in a test binary")
	}
	if _, err := Recent(context.Background(), time.Now()); err != ErrOff {
		t.Errorf("Recent err = %v, want ErrOff", err)
	}
	if v := Current(time.Now()); v.Over || v.Line != "" {
		t.Errorf("Current = %+v, want nothing", v)
	}
}

func TestVolumeOfReadsTheRealVolume(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no statfs")
	}
	v := VolumeOf(t.TempDir())
	if !v.OK || v.UsedPct <= 0 || v.UsedPct > 100 {
		t.Errorf("volume = %+v", v)
	}
}

// The disk reading is the volume that holds Go's build cache (else home):
// on macOS that is the Data volume. `df /` names the sealed system volume,
// whose use (a few percent) is not the one that fills up, so no reading
// statfs's "/" (report 7da6 read `df /` as the card's 15%).
func TestVolumeIsTheCacheFoldersNotRoot(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("GOCACHE", cache)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	var read []string
	prevVol, prevLive := VolumeOf, Live
	t.Cleanup(func() { VolumeOf, Live = prevVol, prevLive })
	VolumeOf = func(p string) Volume {
		read = append(read, p)
		return Volume{UsedPct: 93, FreeGB: 60, OK: true}
	}
	Live = true
	r, err := Measure(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Volume.UsedPct != 93 {
		t.Errorf("measured volume = %+v, want the cache folder's 93%%", r.Volume)
	}
	if v := Current(time.Now()); !v.Over {
		t.Errorf("current = %+v, want over the line at 93%%", v)
	}
	if len(read) != 2 || read[0] != cache || read[1] != cache {
		t.Errorf("statfs paths = %q, want the cache folder %q twice", read, cache)
	}

	// No cache folder: the home folder's volume, still never "/".
	read = nil
	t.Setenv("GOCACHE", "off")
	if _, err := Measure(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if len(read) != 1 || read[0] != home || home == "/" {
		t.Errorf("statfs paths without a cache = %q, want home %q", read, home)
	}
}
