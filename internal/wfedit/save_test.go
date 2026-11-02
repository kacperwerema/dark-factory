package wfedit

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// repo is a throwaway git repository on main holding the fixture at rel,
// committed. The user's git config and hooks stay out of it.
func repo(t *testing.T, rel string) (top, path string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Owner")
	t.Setenv("GIT_AUTHOR_EMAIL", "owner@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Owner")
	t.Setenv("GIT_COMMITTER_EMAIL", "owner@example.com")
	top = t.TempDir()
	run(t, top, "init", "-q", "-b", "main")
	raw, _ := os.ReadFile("testdata/workflow.yaml")
	path = filepath.Join(top, rel)
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, raw, 0o644)
	os.WriteFile(filepath.Join(top, "other.txt"), []byte("x\n"), 0o644)
	run(t, top, "add", ".")
	run(t, top, "commit", "-q", "-m", "start")
	return top, path
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gateOff previews removing the gate into "in progress".
func gateOff(t *testing.T, path string) *Preview {
	t.Helper()
	st, err := Open(path, "in progress")
	if err != nil {
		t.Fatal(err)
	}
	in := st.In[0]
	pv, err := Apply(path, "in progress", st.Hash, Change{Edges: map[int][]tracker.WorkflowAction{in.Index: SetGate(in.Actions, in.To, false)}})
	if err != nil {
		t.Fatal(err)
	}
	return pv
}

func TestSaveCommitsOneFileOnMain(t *testing.T) {
	// A department's own file (F1) in a subfolder: the commit lands in the
	// repo holding it.
	top, path := repo(t, "departments/content/workflow.yaml")
	os.WriteFile(filepath.Join(top, "other.txt"), []byte("staged elsewhere\n"), 0o644)
	run(t, top, "add", "other.txt")

	pv := gateOff(t, path)
	saved, err := Save(pv, `the gate into building slows me down`)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Subject != "workflow(workflow.yaml): in progress: gate into in progress removed" || saved.Commit == "" {
		t.Errorf("saved = %+v", saved)
	}
	if got := run(t, top, "show", "--name-only", "--format=", "HEAD"); got != "departments/content/workflow.yaml" {
		t.Errorf("commit touches %q", got)
	}
	msg := run(t, top, "log", "-1", "--format=%B")
	if !strings.Contains(msg, `Owner: "the gate into building slows me down"`) {
		t.Errorf("message:\n%s", msg)
	}
	if got, _ := os.ReadFile(path); string(got) != string(pv.New) {
		t.Error("file is not the previewed text")
	}
	// What was staged elsewhere stays staged, out of the commit.
	if st := run(t, top, "status", "--porcelain"); st != "M  other.txt" {
		t.Errorf("status after = %q", st)
	}
}

func TestSaveRefusals(t *testing.T) {
	t.Run("empty reason", func(t *testing.T) {
		_, path := repo(t, "workflow.yaml")
		if _, err := Save(gateOff(t, path), "  "); err == nil || !strings.Contains(err.Error(), "reason") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("stale", func(t *testing.T) {
		_, path := repo(t, "workflow.yaml")
		pv := gateOff(t, path)
		raw, _ := os.ReadFile(path)
		os.WriteFile(path, append(raw, '\n'), 0o644)
		if _, err := Save(pv, "why"); !errors.Is(err, ErrStale) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("not main", func(t *testing.T) {
		top, path := repo(t, "workflow.yaml")
		run(t, top, "checkout", "-q", "-b", "work/x")
		_, err := Save(gateOff(t, path), "why")
		if err == nil || !strings.Contains(err.Error(), "is on work/x, not main: switch it to main") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("dirty", func(t *testing.T) {
		_, path := repo(t, "workflow.yaml")
		raw, _ := os.ReadFile(path)
		os.WriteFile(path, append(raw, []byte("# a hand edit\n")...), 0o644)
		_, err := Save(gateOff(t, path), "why")
		if err == nil || !strings.Contains(err.Error(), "workflow.yaml has uncommitted changes: commit or discard them") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("errors", func(t *testing.T) {
		_, path := repo(t, "workflow.yaml")
		st, _ := Open(path, "in progress")
		pv, _ := Apply(path, "in progress", st.Hash, Change{Edges: map[int][]tracker.WorkflowAction{st.Out[0].Index: {{Type: "nope"}}}})
		if _, err := Save(pv, "why"); err == nil || !strings.Contains(err.Error(), "nothing was written") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("commit fails", func(t *testing.T) {
		top, path := repo(t, "workflow.yaml")
		hook := filepath.Join(top, ".git", "hooks", "pre-commit")
		os.WriteFile(hook, []byte("#!/bin/sh\necho no >&2\nexit 1\n"), 0o755)
		old, _ := os.ReadFile(path)
		_, err := Save(gateOff(t, path), "why")
		if err == nil || !strings.Contains(err.Error(), "is back as it was") {
			t.Errorf("err = %v", err)
		}
		if now, _ := os.ReadFile(path); string(now) != string(old) {
			t.Error("file not restored after the failed commit")
		}
	})
}
