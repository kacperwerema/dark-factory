package viewer

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/testbin"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// TestViewerExitsWithParent: a viewer started with ISSUE_VIEWER_EXIT_WITH_PID
// stops by itself once that process is gone, even when it went by SIGKILL
// and ran no cleanup (a test binary killed by go test -timeout). Without the
// variable the viewer would run on (the orphan boards found on the owner's
// Mac, 20 days old).
func TestViewerExitsWithParent(t *testing.T) {
	bin := testbin.Path(t, "./cmd/issue-viewer")
	home := t.TempDir()
	issues := filepath.Join(home, "issues")
	if err := os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := exec.Command("sleep", "600")
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Process.Kill(); _, _ = parent.Process.Wait() })

	port := freePort(t)
	viewer := exec.Command(bin, "-dir", issues, "-docs", filepath.Join(home, "docs"), "-port", fmt.Sprint(port), "-owner-code", "terminal")
	viewer.Dir = home
	viewer.Env = append(os.Environ(), "HOME="+home, "XDG_STATE_HOME="+filepath.Join(home, "state"), "XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		ExitWithPIDEnv+"="+fmt.Sprint(parent.Process.Pid),
		// No reading of this machine: its first health tick would start a
		// detached issue-cli clean that writes into home after the test.
		viewerstate.MachineOffEnv+"=1")
	var vlog strings.Builder
	viewer.Stdout, viewer.Stderr = &vlog, &vlog
	if err := viewer.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- viewer.Wait() }()
	t.Cleanup(func() {
		_ = viewer.Process.Kill()
		if t.Failed() {
			t.Logf("viewer log:\n%s", vlog.String())
		}
	})
	if !waitFor(60*time.Second, func() bool { c, _ := httpGet(fmt.Sprintf("http://127.0.0.1:%d/", port)); return c == 200 }) {
		t.Fatalf("viewer never answered on port %d", port)
	}

	// The parent dies without a word; the viewer must notice by itself.
	_ = parent.Process.Signal(syscall.SIGKILL)
	_, _ = parent.Process.Wait()
	select {
	case <-exited:
	case <-time.After(30 * time.Second):
		t.Fatalf("the viewer still runs 30 s after its parent %d died", parent.Process.Pid)
	}
	if !strings.Contains(vlog.String(), "is gone; stopping") {
		t.Errorf("the viewer did not say why it stopped:\n%s", vlog.String())
	}
}

func httpGet(url string) (int, error) {
	r, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	r.Body.Close()
	return r.StatusCode, nil
}
