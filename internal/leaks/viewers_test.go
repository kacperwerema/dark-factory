package leaks

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/procs"
)

// TestTestViewers: an issue-viewer on a temp folder whose parent is gone
// (pid 1, past the detached age) or whose folder is gone is a leak; the
// owner's viewer, a test's live one and a young detached one are not.
func TestTestViewers(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "projects.yaml"), []byte("projects: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := procs.DetachedMinAge + time.Minute
	mine := []procs.Proc{
		{PID: 101, PPID: 1, Age: old, Command: "/x/issue-viewer -config " + filepath.Join(tmp, "projects.yaml") + " -port 1234"},
		{PID: 102, PPID: 1, Age: old, Command: "/x/issue-viewer -dir=" + filepath.Join(tmp, "gone", "issues")},
		{PID: 103, PPID: 1, Age: old, Command: "/Users/someone/.local/bin/issue-viewer -config /Users/someone/.config/ai-workflow-engine/projects.yaml"},
		{PID: 104, PPID: 4242, Age: old, Command: "/x/issue-viewer -config " + filepath.Join(tmp, "projects.yaml")},
		{PID: 105, PPID: 1, Age: time.Second, Command: "/x/issue-viewer -config " + filepath.Join(tmp, "projects.yaml")},
		{PID: 106, PPID: 1, Age: old, Command: "/x/issue-cli -config " + filepath.Join(tmp, "projects.yaml")},
	}
	got := map[int]bool{}
	for _, f := range testViewers(mine, map[int]bool{}) {
		if f.Kind != KindViewer || f.StopCommand == "" {
			t.Errorf("finding %+v", f)
		}
		got[f.PID] = true
	}
	// 101: old and detached; 102: its folder is gone; 103 is the owner's,
	// 104 has a parent, 105 is young, 106 is no viewer.
	for pid, want := range map[int]bool{101: true, 102: true, 103: false, 104: false, 105: false, 106: false} {
		if got[pid] != want {
			t.Errorf("pid %d: leak=%v, want %v", pid, got[pid], want)
		}
	}
}
