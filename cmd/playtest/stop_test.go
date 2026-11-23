package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/procs"
)

const helperRunnerVar = "PLAYTEST_TEST_RUNNER"

// helperRunner is a runner as main starts it (stopOnSignal) that opens one
// headless browser, writes "<chrome pid> <profile>" to $PLAYTEST_TEST_RUNNER
// and waits.
func helperRunner() bool {
	out := os.Getenv(helperRunnerVar)
	if out == "" {
		return false
	}
	stopOnSignal()
	b, err := NewBrowser(false, 800, 600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	_ = os.WriteFile(out+".tmp", []byte(fmt.Sprintf("%d %s", b.pid, b.profile)), 0o600)
	_ = os.Rename(out+".tmp", out)
	time.Sleep(5 * time.Minute)
	b.Close()
	os.Exit(0)
	return true
}

type helperRun struct {
	cmd     *exec.Cmd
	chrome  int
	profile string
}

func startHelperRunner(t *testing.T) helperRun {
	t.Helper()
	needChrome(t)
	out := filepath.Join(t.TempDir(), "browser")
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperRunnerVar+"="+out)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var r helperRun
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if b, err := os.ReadFile(out); err == nil {
			pid, profile, _ := strings.Cut(string(b), " ")
			r.chrome, _ = strconv.Atoi(pid)
			r.profile = profile
			break
		}
	}
	if r.chrome == 0 {
		_ = cmd.Process.Kill()
		t.Fatal("the runner started no Chrome")
	}
	r.cmd = cmd
	t.Cleanup(func() {
		_ = syscall.Kill(r.chrome, syscall.SIGKILL)
		_ = os.RemoveAll(r.profile)
	})
	if !strings.HasPrefix(filepath.Base(r.profile), fmt.Sprintf("%s%d-", procs.PlaytestProfilePrefix, cmd.Process.Pid)) {
		t.Errorf("profile %s doesn't name the runner (pid %d)", r.profile, cmd.Process.Pid)
	}
	return r
}

// chromeLeft reports whether a Chrome still runs on profile.
func chromeLeft(t *testing.T, profile string) []int {
	t.Helper()
	list, err := procs.Mine()
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, p := range list {
		if strings.Contains(p.Command, "--user-data-dir="+profile) {
			pids = append(pids, p.PID)
		}
	}
	return pids
}

func waitNoChrome(t *testing.T, profile string, within time.Duration) []int {
	t.Helper()
	var left []int
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if left = chromeLeft(t, profile); len(left) == 0 {
			return nil
		}
	}
	return left
}

// TestRunnerStopsChromeOnSignal: SIGTERM (a timeout, a stopped make
// ux-test), SIGINT (Ctrl-C) and SIGHUP (tmux kill-session of the worker)
// each stop Chrome, every process of it, and remove its profile.
func TestRunnerStopsChromeOnSignal(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			r := startHelperRunner(t)
			if err := r.cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			err := r.cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 128+int(sig) {
				t.Errorf("runner exit = %v, want %d", err, 128+int(sig))
			}
			// Stopped before the runner exits: nothing to wait for.
			if left := waitNoChrome(t, r.profile, 2*time.Second); len(left) > 0 {
				t.Fatalf("Chrome left after %s: %v", sig, left)
			}
			if _, err := os.Stat(r.profile); !os.IsNotExist(err) {
				t.Errorf("profile %s still there after %s", r.profile, sig)
			}
		})
	}
}

// TestNextStartReapsAKilledRunnersChrome: SIGKILL runs nothing, so the
// next runner's start finds the Chrome by its profile's runner pid and
// stops it.
func TestNextStartReapsAKilledRunnersChrome(t *testing.T) {
	r := startHelperRunner(t)
	if err := r.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = r.cmd.Wait()
	if procs.Alive(r.chrome) {
		list, _ := procs.Mine()
		found := false
		for _, o := range procs.OrphanChromes(list) {
			found = found || o.PID == r.chrome
		}
		if !found {
			t.Fatalf("Chrome %d of the killed runner is not taken for an orphan", r.chrome)
		}
	}
	reapOrphanChromes()
	if left := waitNoChrome(t, r.profile, 5*time.Second); len(left) > 0 {
		t.Fatalf("Chrome left after the reap: %v", left)
	}
	if _, err := os.Stat(r.profile); !os.IsNotExist(err) {
		t.Errorf("profile %s still there after the reap", r.profile)
	}
}

// TestReapSparesALiveRunnersChrome: another run's Chrome is left alone.
func TestReapSparesALiveRunnersChrome(t *testing.T) {
	r := startHelperRunner(t)
	defer func() { _ = r.cmd.Process.Signal(syscall.SIGTERM); _ = r.cmd.Wait() }()
	reapOrphanChromes()
	if !procs.Alive(r.chrome) {
		t.Fatal("the reap stopped a live runner's Chrome")
	}
}
