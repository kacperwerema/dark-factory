package recent

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
)

func TestMain(m *testing.M) { tmuxguard.Main(m) }

func counter() (*atomic.Int64, func() int64) {
	var n atomic.Int64
	return &n, func() int64 { return n.Add(1) }
}

func TestOffReadsEveryTime(t *testing.T) {
	Keep(0)
	n, read := counter()
	var r Reading[int64]
	r.Get(read)
	r.Get(read)
	if n.Load() != 2 {
		t.Fatalf("reads = %d, want 2 while off", n.Load())
	}
}

func TestOnReadsOnceWithinTheWindow(t *testing.T) {
	Keep(time.Hour)
	t.Cleanup(func() { Keep(0) })
	n, read := counter()
	var r Reading[int64]
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if got := r.Get(read); got != 1 {
				t.Errorf("value = %d, want the one read", got)
			}
		})
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Fatalf("reads = %d, want 1", n.Load())
	}
}

func TestOnReadsAgainAfterTheWindow(t *testing.T) {
	Keep(time.Nanosecond)
	t.Cleanup(func() { Keep(0) })
	n, read := counter()
	var r Reading[int64]
	r.Get(read)
	time.Sleep(time.Millisecond)
	r.Get(read)
	if n.Load() != 2 {
		t.Fatalf("reads = %d, want 2 past the window", n.Load())
	}
}

func TestReadingsKeepOnePerKey(t *testing.T) {
	Keep(time.Hour)
	t.Cleanup(func() { Keep(0) })
	n, read := counter()
	var rs Readings[string, int64]
	a1, a2, b := rs.Get("a", read), rs.Get("a", read), rs.Get("b", read)
	if a1 != a2 || a1 == b || n.Load() != 2 {
		t.Fatalf("a=%d,%d b=%d reads=%d, want a read once and b apart", a1, a2, b, n.Load())
	}
	Keep(0)
	rs.Get("a", read)
	if n.Load() != 3 {
		t.Fatalf("reads = %d, want a fresh read while off", n.Load())
	}
}
