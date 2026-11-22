package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSaveStatsLongIssueName: an issue whose file name is 232 bytes (a
// 240-byte stats sidecar) records its stats; its temp name used to pass
// NAME_MAX (255) and the write failed with "file name too long".
func TestSaveStatsLongIssueName(t *testing.T) {
	dir := t.TempDir()
	issuePath := filepath.Join(dir, strings.Repeat("a", 229)+".md")
	if err := os.WriteFile(issuePath, []byte("---\ntitle: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stat := TransitionStat{From: "idea", To: "in progress", TS: time.Now()}
	if err := AppendTransitionStat(issuePath, stat); err != nil {
		t.Fatalf("AppendTransitionStat: %v", err)
	}
	store, err := LoadStats(issuePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Transitions) != 1 {
		t.Errorf("transitions = %d, want 1", len(store.Transitions))
	}
}
