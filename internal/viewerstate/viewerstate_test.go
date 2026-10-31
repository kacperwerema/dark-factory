package viewerstate

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteRead(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if _, err := Read(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("before Write: want ErrNotExist, got %v", err)
	}
	want := State{Config: "/w/projects.yaml", URL: "http://127.0.0.1:8080", PID: 7, Started: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)}
	if err := Write(want); err != nil {
		t.Fatal(err)
	}
	got, err := Read()
	if err != nil || got != want {
		t.Fatalf("Read = %+v, %v; want %+v", got, err, want)
	}
	entries, _ := os.ReadDir(Dir())
	if len(entries) != 1 {
		t.Fatalf("Write left temp files behind: %v", entries)
	}
}

func TestPathDefaultsUnderHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/h")
	if got, want := Path(), filepath.Join("/h", ".local", "state", "ai-workflow-engine", "viewer.json"); got != want {
		t.Fatalf("Path = %s, want %s", got, want)
	}
}

func TestBaseURL(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	served := filepath.Join(dir, "projects.yaml")
	other := filepath.Join(dir, "other.yaml")
	for _, p := range []string{served, other} {
		if err := os.WriteFile(p, []byte("projects: []\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if got := BaseURL("", served); got != DefaultURL {
		t.Fatalf("no viewer.json: got %q, want %q", got, DefaultURL)
	}
	if err := Write(State{Config: served, URL: "http://127.0.0.1:9090/"}); err != nil {
		t.Fatal(err)
	}
	if got := BaseURL("", served); got != "http://127.0.0.1:9090" {
		t.Fatalf("viewer.json for this config: got %q", got)
	}
	if got := BaseURL("", other); got != DefaultURL {
		t.Fatalf("viewer.json for another config must be ignored: got %q", got)
	}
	if got := BaseURL("", ""); got != DefaultURL {
		t.Fatalf("no config: got %q", got)
	}
	if got := BaseURL("https://issues.example.com/viewer/", served); got != "https://issues.example.com/viewer" {
		t.Fatalf("viewer_url wins: got %q", got)
	}
}

func TestClaim(t *testing.T) {
	sandbox, shared := t.TempDir(), t.TempDir()
	old := SandboxRoots
	SandboxRoots = func() []string { return []string{sandbox} }
	t.Cleanup(func() { SandboxRoots = old })
	t.Setenv("XDG_STATE_HOME", shared)
	owner := filepath.Join(shared, "projects.yaml")
	if err := os.WriteFile(owner, []byte("projects: []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(sandbox, "sub", "proj-18752.yaml")

	if ok, why := Claim(scratch, 99); ok || !strings.Contains(why, "temp folder") {
		t.Fatalf("sandbox config with shared state: Claim = %v %q, want refused", ok, why)
	}
	if ok, _ := Claim(owner, 99); !ok {
		t.Fatal("no viewer.json yet: the owner's viewer must record itself")
	}

	// A live viewer (this test process) holds the record for owner.
	if err := Write(State{Config: owner, URL: "http://127.0.0.1:8090", PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(shared, "other.yaml")
	if ok, why := Claim(other, os.Getpid()+1); ok || !strings.Contains(why, "8090") {
		t.Fatalf("another live viewer's record: Claim = %v %q, want refused naming it", ok, why)
	}
	if ok, _ := Claim(owner, os.Getpid()+1); !ok {
		t.Fatal("a restart for the same config must record itself")
	}
	// The holder is gone: the record is free.
	if err := Write(State{Config: owner, PID: deadPID(t)}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Claim(other, os.Getpid()); !ok {
		t.Fatal("a dead viewer's record must be free to take")
	}

	// A sandbox with its own state dir records into it.
	t.Setenv("XDG_STATE_HOME", filepath.Join(sandbox, "state"))
	if ok, why := Claim(scratch, 99); !ok {
		t.Fatalf("sandbox config with sandbox state: refused: %s", why)
	}
}

func TestAlive(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Fatal("this process is alive")
	}
	if Alive(0) || Alive(-1) || Alive(deadPID(t)) {
		t.Fatal("0, -1 and an exited process are not alive")
	}
}

// deadPID is the pid of a process that has exited and been reaped.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}
