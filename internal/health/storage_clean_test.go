package health

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clean"
	"github.com/kacperwerema/dark-factory/internal/diskuse"
)

// The storage family shows the last nightly clean: what it freed, with the
// volume before and after in the metric's help.
func TestStorageShowsTheLastClean(t *testing.T) {
	r := clean.Result{At: time.Date(2026, 11, 18, 4, 0, 0, 0, time.Local), Areas: []clean.Area{{FreedBytes: 230 << 30}, {FreedBytes: 3 << 30}},
		Before: diskuse.Volume{UsedPct: 93, OK: true}, After: diskuse.Volume{UsedPct: 71, OK: true}}
	m := cleanMetric(r)
	if m.Name != "storage.clean_freed_mb" || m.Value != 233*1024 || m.Unit != UnitMB {
		t.Errorf("metric = %+v", m)
	}
	if !strings.Contains(m.Help, "issue-cli clean") || !strings.Contains(m.Help, "freed 233.0 GB, the volume 93% → 71% used") {
		t.Errorf("help = %q", m.Help)
	}
}
